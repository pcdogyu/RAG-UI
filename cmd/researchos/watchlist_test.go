package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const validWatchlistJSON = `{
  "crypto":[
    {"label":"Bear","content":"BTC 跌破关键支撑后需观察流动性。"},
    {"label":"Bull","content":"ETF 流入恢复将改善风险偏好。"},
    {"label":"Base","content":"波动收敛时等待方向确认。"}
  ],
  "us_equities":[
    {"label":"Base","content":"财报与利率数据之间维持震荡。"},
    {"label":"Bull","content":"通胀回落有利成长股估值。"},
    {"label":"Bear","content":"收益率上行将压制高估值板块。"}
  ],
  "news":[
    {"title":"宏观数据公布","summary":"市场重新评估利率路径。","source":"Reuters","published_at":"2026-08-20 10:30 UTC","url":"https://example.com/story"},
    {"title":"无效链接测试","summary":"链接会被安全过滤。","source":"AP","published_at":"2026-08-20 10:10 UTC","url":"javascript:alert(1)"}
  ]
}`

func TestParseWatchlistBriefValidatesScenariosAndURLs(t *testing.T) {
	payload, err := parseWatchlistBrief("```json\n" + watchlistJSONWithNewsCount(t, watchlistBriefMaxNews) + "\n```")
	if err != nil {
		t.Fatalf("parseWatchlistBrief() error = %v", err)
	}
	if got := strings.Join([]string{payload.Crypto[0].Label, payload.Crypto[1].Label, payload.Crypto[2].Label}, ","); got != "Bull,Base,Bear" {
		t.Fatalf("crypto labels = %s", got)
	}
	if payload.News[0].URL != "https://example.com/story" || payload.News[1].URL != "" {
		t.Fatalf("unexpected URLs: %+v", payload.News)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseWatchlistBrief(string(encoded)); err != nil {
		t.Fatalf("expected %d news items to be accepted: %v", watchlistBriefMaxNews, err)
	}
	if _, err := parseWatchlistBrief(watchlistJSONWithNewsCount(t, watchlistBriefMaxNews-1)); err == nil {
		t.Fatalf("expected %d news items to be rejected", watchlistBriefMaxNews-1)
	}
	payload.News = append(payload.News, payload.News[0])
	encoded, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseWatchlistBrief(string(encoded)); err == nil {
		t.Fatalf("expected %d news items to be rejected", watchlistBriefMaxNews+1)
	}
	if _, err := parseWatchlistBrief(`{"crypto":[],"us_equities":[],"news":[]}`); err == nil {
		t.Fatal("expected invalid brief error")
	}
}

func TestWatchlistBriefUsesRealtimeScopeAndCaches(t *testing.T) {
	agentChats := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/login":
			_, _ = io.WriteString(w, `{"success":true,"token":"test-token","active_tenant":{"id":"tenant-a"}}`)
		case "/api/v1/agents/agent-1":
			_, _ = io.WriteString(w, `{"data":{"config":{"knowledge_bases":["kb-internal"],"allowed_tools":["knowledge_search","web_search"]}}}`)
		case "/api/v1/sessions":
			var payload struct {
				AgentConfig agentConfig `json:"agent_config"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.AgentConfig.KnowledgeBases) != 0 || strings.Join(payload.AgentConfig.AllowedTools, ",") != "web_search" {
				t.Fatalf("watchlist leaked internal config: %+v", payload.AgentConfig)
			}
			_, _ = io.WriteString(w, `{"data":{"id":"session-1"}}`)
		case "/api/v1/agent-chat/session-1":
			agentChats++
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["web_search_enabled"] != true {
				t.Fatalf("web search not enabled: %+v", payload)
			}
			if _, exists := payload["knowledge_base_ids"]; exists {
				t.Fatalf("knowledge_base_ids must be omitted: %+v", payload)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: completed\ndata: {}\n\n")
		case "/api/v1/messages/session-1/load":
			_, _ = io.WriteString(w, `{"data":[{"role":"assistant","content":`+strconvQuote(watchlistJSONWithNewsCount(t, watchlistBriefMaxNews))+`,"is_completed":true}]}`)
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	service := newWatchlistBriefService(NewWeKnoraClient(WeKnoraConfig{BaseURL: server.URL, Email: "researcher@example.com", Password: "super-secret", AgentID: "agent-1", KnowledgeBaseID: "kb-internal"}))
	first, err := service.Brief(context.Background(), false)
	if err != nil || first.Cached || len(first.News) != watchlistBriefMaxNews {
		t.Fatalf("first brief = %+v, %v", first, err)
	}
	second, err := service.Brief(context.Background(), false)
	if err != nil || !second.Cached || agentChats != 1 {
		t.Fatalf("cached brief = %+v, %v, calls=%d", second, err, agentChats)
	}
	if _, err := service.Brief(context.Background(), true); err != nil || agentChats != 2 {
		t.Fatalf("forced brief error=%v calls=%d", err, agentChats)
	}
}

func TestWatchlistBriefHandlerErrors(t *testing.T) {
	unconfigured := newWatchlistBriefService(NewWeKnoraClient(WeKnoraConfig{}))
	response := httptest.NewRecorder()
	unconfigured.serveBrief(response, httptest.NewRequest(http.MethodGet, "/api/v1/watchlist/brief", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured response = %d", response.Code)
	}
	response = httptest.NewRecorder()
	unconfigured.serveBrief(response, httptest.NewRequest(http.MethodGet, "/api/v1/watchlist/brief?refresh=now", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid refresh response = %d", response.Code)
	}
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func watchlistJSONWithNewsCount(t *testing.T, count int) string {
	t.Helper()
	var payload watchlistBriefPayload
	if err := json.Unmarshal([]byte(validWatchlistJSON), &payload); err != nil {
		t.Fatalf("unmarshal valid watchlist JSON: %v", err)
	}
	for len(payload.News) < count {
		payload.News = append(payload.News, payload.News[0])
	}
	payload.News = payload.News[:count]
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal watchlist JSON with %d news items: %v", count, err)
	}
	return string(encoded)
}
