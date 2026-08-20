package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// WeKnoraConfig 从运行环境读取；账号、密码和 token 均不会写入文件或响应给浏览器。
type WeKnoraConfig struct {
	BaseURL         string
	ConsoleURL      string
	Email           string
	Password        string
	AgentID         string
	KnowledgeBaseID string
	UploadMaxBytes  int64
}

func loadWeKnoraConfig() WeKnoraConfig {
	baseURL := strings.TrimRight(os.Getenv("WEKNORA_BASE_URL"), "/")
	if baseURL == "" {
		baseURL = "http://10.15.0.27"
	}
	return WeKnoraConfig{
		BaseURL:         baseURL,
		ConsoleURL:      strings.TrimRight(os.Getenv("WEKNORA_CONSOLE_URL"), "/"),
		Email:           os.Getenv("WEKNORA_EMAIL"),
		Password:        os.Getenv("WEKNORA_PASSWORD"),
		AgentID:         valueOrDefault(os.Getenv("WEKNORA_AGENT_ID"), "30a2f66f-7650-4cb0-a6f8-e64981b8a95d"),
		KnowledgeBaseID: os.Getenv("WEKNORA_KNOWLEDGE_BASE_ID"),
		UploadMaxBytes:  loadWeKnoraUploadMaxBytes(),
	}
}

func valueOrDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func (c WeKnoraConfig) enabled() bool {
	return c.BaseURL != "" && c.Email != "" && c.Password != "" && c.AgentID != ""
}

type WeKnoraClient struct {
	config WeKnoraConfig
	http   *http.Client
}

func NewWeKnoraClient(config WeKnoraConfig) *WeKnoraClient {
	return &WeKnoraClient{config: config, http: &http.Client{Timeout: 100 * time.Second}}
}

type weKnoraLogin struct {
	Success      bool   `json:"success"`
	Message      string `json:"message"`
	Token        string `json:"token"`
	ActiveTenant struct {
		ID any `json:"id"`
	} `json:"active_tenant"`
}

type agentConfig struct {
	MaxIterations  int      `json:"max_iterations"`
	Temperature    float64  `json:"temperature"`
	KnowledgeBases []string `json:"knowledge_bases"`
	AllowedTools   []string `json:"allowed_tools"`
}

type agentDetail struct {
	Data struct {
		Config agentConfig `json:"config"`
	} `json:"data"`
}

type sessionResponse struct {
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

type weKnoraReference struct {
	ID             string `json:"id"`
	KnowledgeID    string `json:"knowledge_id"`
	KnowledgeTitle string `json:"knowledge_title"`
	KnowledgeFile  string `json:"knowledge_filename"`
	Content        string `json:"content"`
	Metadata       struct {
		URL string `json:"url"`
	} `json:"metadata"`
}

type storedMessage struct {
	Role       string             `json:"role"`
	Content    string             `json:"content"`
	Completed  bool               `json:"is_completed"`
	References []weKnoraReference `json:"knowledge_references"`
	AgentSteps []weKnoraAgentStep `json:"agent_steps"`
}

type weKnoraAgentStep struct {
	Timestamp time.Time              `json:"timestamp"`
	ToolCalls []weKnoraAgentToolCall `json:"tool_calls"`
}

type weKnoraAgentToolCall struct {
	Name     string `json:"name"`
	Duration int64  `json:"duration"`
	Result   struct {
		Success *bool `json:"success"`
	} `json:"result"`
}

type messagesResponse struct {
	Data []storedMessage `json:"data"`
}

type WeKnoraAnswer struct {
	Conclusion string             `json:"conclusion"`
	Citations  []ResearchCitation `json:"citations"`
	ToolCalls  []ResearchToolCall `json:"tool_calls"`
}

type ResearchCitation struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url,omitempty"`
	Source  string `json:"source"`
	ChunkID string `json:"chunk_id,omitempty"`
}

type researchScope struct {
	Label           string
	Prompt          string
	UseInternal     bool
	UseExternalLive bool
}

var internalToolNames = map[string]struct{}{
	"grep_chunks": {}, "knowledge_search": {}, "list_knowledge_chunks": {}, "query_knowledge_graph": {}, "get_document_info": {}, "database_query": {},
}

var knowledgeTagPattern = regexp.MustCompile(`(?s)<kb\s+([^>]*?)/?\s*>`)
var knowledgeTagAttributePattern = regexp.MustCompile(`([a-zA-Z_]+)="([^"]*)"`)

// ResearchToolCall is a non-sensitive audit entry for a real research request.
// It deliberately excludes credentials, tokens, session IDs, and request text.
type ResearchToolCall struct {
	Name       string    `json:"name"`
	Detail     string    `json:"detail"`
	Source     string    `json:"source"`
	StartedAt  time.Time `json:"started_at"`
	DurationMS int64     `json:"duration_ms"`
	Status     string    `json:"status"`
}

type researchCallRecorder struct {
	calls []ResearchToolCall
}

func (r *researchCallRecorder) record(name, detail string, action func() error) error {
	startedAt := time.Now().UTC()
	err := action()
	status := "completed"
	if err != nil {
		status = "failed"
	}
	r.calls = append(r.calls, ResearchToolCall{
		Name:       name,
		Detail:     detail,
		Source:     "gateway",
		StartedAt:  startedAt,
		DurationMS: time.Since(startedAt).Milliseconds(),
		Status:     status,
	})
	return err
}

func (c *WeKnoraClient) Ask(ctx context.Context, question, scope string) (WeKnoraAnswer, error) {
	if !c.config.enabled() {
		return WeKnoraAnswer{}, fmt.Errorf("WeKnora is not configured")
	}
	policy, err := researchScopeFor(scope)
	if err != nil {
		return WeKnoraAnswer{}, err
	}
	recorder := researchCallRecorder{}
	var login weKnoraLogin
	err = recorder.record("身份授权", "向 HYGR 智能问答建立当前请求的授权上下文", func() error {
		var loginErr error
		login, loginErr = c.login(ctx)
		return loginErr
	})
	if err != nil {
		return WeKnoraAnswer{ToolCalls: recorder.calls}, err
	}
	var config agentConfig
	err = recorder.record("读取智能体配置", "加载已授权的 HYGR 投研工作台工具与知识库配置", func() error {
		var configErr error
		config, configErr = c.agentConfig(ctx, login)
		return configErr
	})
	if err != nil {
		return WeKnoraAnswer{ToolCalls: recorder.calls}, err
	}
	if policy.UseInternal && c.config.KnowledgeBaseID != "" {
		config.KnowledgeBases = []string{c.config.KnowledgeBaseID}
	}
	if config.MaxIterations == 0 {
		config.MaxIterations = 8
	}
	if len(config.AllowedTools) == 0 {
		config.AllowedTools = []string{"knowledge_search"}
	}
	applyResearchScope(&config, policy)

	var sessionID string
	err = recorder.record("创建研究会话", "为本次问题建立独立的 HYGR 研究会话", func() error {
		var sessionErr error
		sessionID, sessionErr = c.createSession(ctx, login, config)
		return sessionErr
	})
	if err != nil {
		return WeKnoraAnswer{ToolCalls: recorder.calls}, err
	}
	tools := strings.Join(config.AllowedTools, "、")
	if tools == "" {
		tools = "由智能体默认配置决定"
	}
	err = recorder.record("智能体检索与生成", fmt.Sprintf("检索范围：%s；内部知识库：%d 个；可用工具：%s", policy.Label, len(config.KnowledgeBases), tools), func() error {
		return c.streamAgentAnswer(ctx, login, sessionID, policy.Prompt+"\n\n问题："+question, config.KnowledgeBases, policy.UseExternalLive)
	})
	if err != nil {
		return WeKnoraAnswer{ToolCalls: recorder.calls}, err
	}
	var answer WeKnoraAnswer
	err = recorder.record("加载研究回答", "读取已完成的回答与可展示引用", func() error {
		var answerErr error
		answer, answerErr = c.loadAnswer(ctx, login, sessionID)
		return answerErr
	})
	answer.ToolCalls = append(answer.ToolCalls, recorder.calls...)
	if err != nil {
		return answer, err
	}
	return answer, nil
}

func researchScopeFor(scope string) (researchScope, error) {
	switch scope {
	case "仅内部":
		return researchScope{Label: scope, UseInternal: true, Prompt: "请仅使用机构内部知识库回答。禁止联网、实时搜索、外部 MCP 或外部资料；若内部资料不足，请明确说明。"}, nil
	case "内部 + 实时":
		return researchScope{Label: scope, UseInternal: true, UseExternalLive: true, Prompt: "请同时检索机构内部知识库与外部实时资料，并区分两类来源及时间。"}, nil
	case "实时":
		return researchScope{Label: scope, UseExternalLive: true, Prompt: "请仅使用外部实时信息、原始来源或联网检索回答。禁止查询、引用或基于机构内部知识库推断；请标注外部来源与时间。"}, nil
	default:
		return researchScope{}, fmt.Errorf("unsupported research scope %q", scope)
	}
}

func applyResearchScope(config *agentConfig, policy researchScope) {
	if !policy.UseInternal {
		config.KnowledgeBases = []string{}
		externalTools := make([]string, 0, len(config.AllowedTools))
		for _, tool := range config.AllowedTools {
			if _, internal := internalToolNames[tool]; !internal {
				externalTools = append(externalTools, tool)
			}
		}
		config.AllowedTools = externalTools
	}
}

func (c *WeKnoraClient) login(ctx context.Context) (weKnoraLogin, error) {
	var login weKnoraLogin
	err := c.json(ctx, http.MethodPost, "/api/v1/auth/login", nil, map[string]string{"email": c.config.Email, "password": c.config.Password}, &login)
	if err != nil {
		return login, fmt.Errorf("WeKnora login: %w", err)
	}
	if !login.Success || login.Token == "" {
		return login, fmt.Errorf("WeKnora login rejected: %s", login.Message)
	}
	return login, nil
}

func (c *WeKnoraClient) agentConfig(ctx context.Context, login weKnoraLogin) (agentConfig, error) {
	var response agentDetail
	err := c.json(ctx, http.MethodGet, "/api/v1/agents/"+url.PathEscape(c.config.AgentID), &login, nil, &response)
	return response.Data.Config, err
}

func (c *WeKnoraClient) createSession(ctx context.Context, login weKnoraLogin, config agentConfig) (string, error) {
	var response sessionResponse
	err := c.json(ctx, http.MethodPost, "/api/v1/sessions", &login, map[string]any{"agent_config": config}, &response)
	if err != nil {
		return "", err
	}
	if response.Data.ID == "" {
		return "", fmt.Errorf("WeKnora did not return a session id")
	}
	return response.Data.ID, nil
}

func (c *WeKnoraClient) streamAgentAnswer(ctx context.Context, login weKnoraLogin, sessionID, question string, knowledgeBases []string, useExternalLive bool) error {
	payload := map[string]any{
		"query": question, "agent_enabled": true, "agent_id": c.config.AgentID,
		"web_search_enabled": useExternalLive, "channel": "web",
	}
	if len(knowledgeBases) > 0 {
		payload["knowledge_base_ids"] = knowledgeBases
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL+"/api/v1/agent-chat/"+url.PathEscape(sessionID), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	c.authorize(request, login)
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		problem, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("WeKnora agent chat returned %s: %s", response.Status, strings.TrimSpace(string(problem)))
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		return fmt.Errorf("WeKnora agent chat did not return an event stream")
	}
	_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 10<<20))
	return err
}

func (c *WeKnoraClient) loadAnswer(ctx context.Context, login weKnoraLogin, sessionID string) (WeKnoraAnswer, error) {
	var messages messagesResponse
	path := "/api/v1/messages/" + url.PathEscape(sessionID) + "/load?limit=20"
	if err := c.json(ctx, http.MethodGet, path, &login, nil, &messages); err != nil {
		return WeKnoraAnswer{}, err
	}
	for index := len(messages.Data) - 1; index >= 0; index-- {
		message := messages.Data[index]
		if message.Role != "assistant" || !message.Completed {
			continue
		}
		conclusion, taggedCitations := formatInternalKnowledgeTags(message.Content)
		answer := WeKnoraAnswer{Conclusion: conclusion, Citations: taggedCitations, ToolCalls: toolCallsFromAgentSteps(message.AgentSteps)}
		for _, reference := range message.References {
			title := firstNonEmpty(reference.KnowledgeTitle, reference.KnowledgeFile, reference.ID, reference.KnowledgeID)
			answer.Citations = appendUniqueCitation(answer.Citations, ResearchCitation{ID: firstNonEmpty(reference.ID, reference.KnowledgeID), Title: title, URL: reference.Metadata.URL, Source: "internal"})
		}
		return answer, nil
	}
	return WeKnoraAnswer{}, fmt.Errorf("WeKnora returned no completed assistant answer")
}

func formatInternalKnowledgeTags(content string) (string, []ResearchCitation) {
	citations := make([]ResearchCitation, 0)
	formatted := knowledgeTagPattern.ReplaceAllStringFunc(content, func(tag string) string {
		attributes := map[string]string{}
		for _, match := range knowledgeTagAttributePattern.FindAllStringSubmatch(tag, -1) {
			attributes[match[1]] = match[2]
		}
		title := firstNonEmpty(attributes["doc"], attributes["title"], "机构内部资料")
		citations = appendUniqueCitation(citations, ResearchCitation{
			ID: firstNonEmpty(attributes["chunk_id"], attributes["kb_id"], title), Title: title, Source: "internal", ChunkID: attributes["chunk_id"],
		})
		return "〔内部资料〕"
	})
	return strings.TrimSpace(formatted), citations
}

func appendUniqueCitation(citations []ResearchCitation, next ResearchCitation) []ResearchCitation {
	for _, citation := range citations {
		if citation.ID == next.ID && citation.Title == next.Title {
			return citations
		}
	}
	return append(citations, next)
}

func toolCallsFromAgentSteps(steps []weKnoraAgentStep) []ResearchToolCall {
	toolCalls := make([]ResearchToolCall, 0)
	for _, step := range steps {
		for _, toolCall := range step.ToolCalls {
			name := strings.TrimSpace(toolCall.Name)
			if name == "" {
				continue
			}
			status := "completed"
			if toolCall.Result.Success != nil && !*toolCall.Result.Success {
				status = "failed"
			}
			toolCalls = append(toolCalls, ResearchToolCall{
				Name:       name,
				Detail:     "由 WeKnora 智能体实际执行的工具调用",
				Source:     "agent",
				StartedAt:  step.Timestamp,
				DurationMS: toolCall.Duration,
				Status:     status,
			})
		}
	}
	return toolCalls
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "WeKnora 引用"
}

func (c *WeKnoraClient) json(ctx context.Context, method, path string, login *weKnoraLogin, payload any, target any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.config.BaseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if login != nil {
		c.authorize(request, *login)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		problem, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("WeKnora returned %s: %s", response.Status, strings.TrimSpace(string(problem)))
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func (c *WeKnoraClient) authorize(request *http.Request, login weKnoraLogin) {
	request.Header.Set("Authorization", "Bearer "+login.Token)
	if login.ActiveTenant.ID != nil {
		request.Header.Set("X-Tenant-ID", fmt.Sprint(login.ActiveTenant.ID))
	}
}
