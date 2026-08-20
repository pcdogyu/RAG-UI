package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAskReturnsToolCallHistoryWithoutSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/login":
			_, _ = w.Write([]byte(`{"success":true,"token":"test-token","active_tenant":{"id":"tenant-a"}}`))
		case "/api/v1/agents/agent-1":
			_, _ = w.Write([]byte(`{"data":{"config":{"knowledge_bases":["kb-1"],"allowed_tools":["knowledge_search"]}}}`))
		case "/api/v1/sessions":
			_, _ = w.Write([]byte(`{"data":{"id":"session-1"}}`))
		case "/api/v1/agent-chat/session-1":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: completed\ndata: {}\n\n"))
		case "/api/v1/messages/session-1/load":
			_, _ = w.Write([]byte(`{"data":[{"role":"assistant","content":"研究结论","is_completed":true,"knowledge_references":[{"id":"ref-1","knowledge_title":"研究报告"}],"agent_steps":[{"timestamp":"2026-08-20T10:00:00Z","tool_calls":[{"name":"新浪财经MCP","duration":125,"result":{"success":true}}]}]}]}`))
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewWeKnoraClient(WeKnoraConfig{
		BaseURL: server.URL, Email: "researcher@example.com", Password: "super-secret", AgentID: "agent-1",
	})
	answer, err := client.Ask(context.Background(), "测试问题", "内部 + 实时")
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if answer.Conclusion != "研究结论" || len(answer.ToolCalls) != 6 {
		t.Fatalf("unexpected answer: %+v", answer)
	}
	want := []string{"新浪财经MCP", "身份授权", "读取智能体配置", "创建研究会话", "智能体检索与生成", "加载研究回答"}
	for index, call := range answer.ToolCalls {
		if call.Name != want[index] || call.Status != "completed" || call.StartedAt.IsZero() || call.DurationMS < 0 {
			t.Fatalf("call %d = %+v", index, call)
		}
	}
	if answer.ToolCalls[0].Source != "agent" || answer.ToolCalls[1].Source != "gateway" {
		t.Fatalf("unexpected call sources: %+v", answer.ToolCalls)
	}
	encoded, err := json.Marshal(answer)
	if err != nil {
		t.Fatalf("marshal answer: %v", err)
	}
	if strings.Contains(string(encoded), "super-secret") || strings.Contains(string(encoded), "test-token") || strings.Contains(string(encoded), "session-1") {
		t.Fatalf("tool history leaked sensitive request data: %s", encoded)
	}
}
