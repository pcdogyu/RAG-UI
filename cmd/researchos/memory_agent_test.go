package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMemoryAgentSessionUsesDedicatedAgentAndOpaqueToken(t *testing.T) {
	var chatAgentID string
	var webSearch bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/login":
			_, _ = w.Write([]byte(`{"success":true,"token":"test-token","active_tenant":{"id":"tenant-a"}}`))
		case "/api/v1/agents/memory-agent":
			_, _ = w.Write([]byte(`{"data":{"id":"memory-agent","name":"HYGR 研究记忆","description":"机构研究材料检索","avatar":"◈","config":{"agent_mode":"smart-reasoning","knowledge_bases":["kb-1"],"allowed_tools":["knowledge_search"],"citation_enabled":true,"web_search_enabled":true,"multi_turn_enabled":true,"retain_retrieval_history":true}}}`))
		case "/api/v1/sessions":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode session payload: %v", err)
			}
			config, _ := payload["agent_config"].(map[string]any)
			if config["kb_selection_mode"] != "selected" {
				t.Fatalf("session config = %#v", config)
			}
			_, _ = w.Write([]byte(`{"data":{"id":"raw-session-1"}}`))
		case "/api/v1/agent-chat/raw-session-1":
			var payload struct {
				AgentID          string `json:"agent_id"`
				WebSearchEnabled bool   `json:"web_search_enabled"`
				Query            string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode chat payload: %v", err)
			}
			chatAgentID, webSearch = payload.AgentID, payload.WebSearchEnabled
			if payload.Query != "追溯 ETH 命题" {
				t.Fatalf("query = %q", payload.Query)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: completed\ndata: {}\n\n"))
		case "/api/v1/messages/raw-session-1/load":
			_, _ = w.Write([]byte(`{"data":[{"role":"user","content":"追溯 ETH 命题"},{"role":"assistant","content":"结论 <kb doc=\"机构周报\" chunk_id=\"chunk-1\" />","is_completed":true,"knowledge_references":[{"id":"ref-1","knowledge_title":"机构周报"}]}]}`))
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	service := newMemoryAgentService(NewWeKnoraClient(WeKnoraConfig{BaseURL: server.URL, Email: "researcher@example.com", Password: "test-secret", MemoryAgentID: "memory-agent", KnowledgeBaseID: "kb-1"}))
	rawSessionID, err := service.createSession(context.Background())
	if err != nil || rawSessionID != "raw-session-1" {
		t.Fatalf("createSession = %q, %v", rawSessionID, err)
	}
	token, err := service.codec.seal(rawSessionID)
	if err != nil || token == rawSessionID || strings.Contains(token, rawSessionID) {
		t.Fatalf("opaque session token = %q, %v", token, err)
	}
	if opened, err := service.codec.open(token); err != nil || opened != rawSessionID {
		t.Fatalf("open token = %q, %v", opened, err)
	}
	if _, err := service.codec.open(token + "x"); err == nil {
		t.Fatal("tampered token was accepted")
	}

	answer, policy, err := service.ask(context.Background(), rawSessionID, "追溯 ETH 命题", memoryScopeInternalLive)
	if err != nil || policy.Label != "内部 + 实时网页" || answer.Conclusion == "" {
		t.Fatalf("ask = %+v, %+v, %v", answer, policy, err)
	}
	if chatAgentID != "memory-agent" || !webSearch {
		t.Fatalf("chat agent=%q web=%v", chatAgentID, webSearch)
	}
	messages, err := service.loadMessages(context.Background(), rawSessionID)
	if err != nil || len(messages) != 2 || messages[1].Answer == nil || len(messages[1].Answer.Citations) < 1 {
		t.Fatalf("messages = %+v, %v", messages, err)
	}
}

func TestMemoryScopeAndHandlerValidation(t *testing.T) {
	for _, scope := range []string{memoryScopeInternal, memoryScopeInternalLive} {
		if _, err := memoryScope(scope); err != nil {
			t.Fatalf("memoryScope(%q): %v", scope, err)
		}
	}
	if _, err := memoryScope("实时"); err == nil {
		t.Fatal("invalid scope was accepted")
	}
	service := newMemoryAgentService(NewWeKnoraClient(WeKnoraConfig{}))
	response := httptest.NewRecorder()
	service.serveInfo(response, httptest.NewRequest(http.MethodGet, "/api/v1/research/memory-agent", nil))
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "Password") {
		t.Fatalf("unconfigured response = %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	service.serveMessages(response, httptest.NewRequest(http.MethodGet, "/api/v1/research/memory-agent/sessions/tampered", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("bad token response = %d %s", response.Code, response.Body.String())
	}
}

func TestMemoryAgentDirectoryReturnsSafePlatformAgents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/login":
			_, _ = w.Write([]byte(`{"success":true,"token":"test-token","active_tenant":{"id":"tenant-a"}}`))
		case "/api/v1/agents":
			_, _ = w.Write([]byte(`{"data":[{"id":"builtin-quick-answer","name":"快速问答","description":"RAG 问答","avatar":"◈","is_builtin":true,"tenant_id":"system","config":{"secret":"must-not-leak"}},{"id":"hygr-memory","name":"HYGR 研究记忆","description":"研究追溯","avatar":"◆","is_builtin":false,"created_by":"admin"},{"id":"","name":"损坏记录","is_builtin":false}]}`))
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	service := newMemoryAgentService(NewWeKnoraClient(WeKnoraConfig{BaseURL: server.URL, ConsoleURL: "https://console.example/", Email: "researcher@example.com", Password: "test-secret"}))
	response := httptest.NewRecorder()
	service.serveDirectory(response, httptest.NewRequest(http.MethodGet, "/api/v1/research/memory-agents", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("directory response = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "must-not-leak") || strings.Contains(response.Body.String(), "tenant_id") || strings.Contains(response.Body.String(), "created_by") {
		t.Fatalf("directory leaked upstream fields: %s", response.Body.String())
	}
	var body struct {
		Agents     []memoryAgentDirectoryItem `json:"agents"`
		ConsoleURL string                     `json:"console_url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode directory response: %v", err)
	}
	if len(body.Agents) != 2 || !body.Agents[0].IsBuiltin || body.Agents[1].IsBuiltin {
		t.Fatalf("directory agents = %#v", body.Agents)
	}
	if body.ConsoleURL != "https://console.example/platform/agents" {
		t.Fatalf("console URL = %q", body.ConsoleURL)
	}

	unconfigured := newMemoryAgentService(NewWeKnoraClient(WeKnoraConfig{}))
	response = httptest.NewRecorder()
	unconfigured.serveDirectory(response, httptest.NewRequest(http.MethodGet, "/api/v1/research/memory-agents", nil))
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "Password") {
		t.Fatalf("unconfigured directory = %d %s", response.Code, response.Body.String())
	}
}
