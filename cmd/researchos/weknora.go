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
	"strings"
	"time"
)

// WeKnoraConfig 从运行环境读取；账号、密码和 token 均不会写入文件或响应给浏览器。
type WeKnoraConfig struct {
	BaseURL         string
	Email           string
	Password        string
	AgentID         string
	KnowledgeBaseID string
}

func loadWeKnoraConfig() WeKnoraConfig {
	baseURL := strings.TrimRight(os.Getenv("WEKNORA_BASE_URL"), "/")
	if baseURL == "" {
		baseURL = "http://10.15.0.27"
	}
	return WeKnoraConfig{
		BaseURL:         baseURL,
		Email:           os.Getenv("WEKNORA_EMAIL"),
		Password:        os.Getenv("WEKNORA_PASSWORD"),
		AgentID:         valueOrDefault(os.Getenv("WEKNORA_AGENT_ID"), "30a2f66f-7650-4cb0-a6f8-e64981b8a95d"),
		KnowledgeBaseID: os.Getenv("WEKNORA_KNOWLEDGE_BASE_ID"),
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
}

type messagesResponse struct {
	Data []storedMessage `json:"data"`
}

type WeKnoraAnswer struct {
	Conclusion string             `json:"conclusion"`
	Citations  []ResearchCitation `json:"citations"`
}

type ResearchCitation struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
}

func (c *WeKnoraClient) Ask(ctx context.Context, question, scope string) (WeKnoraAnswer, error) {
	if !c.config.enabled() {
		return WeKnoraAnswer{}, fmt.Errorf("WeKnora is not configured")
	}
	login, err := c.login(ctx)
	if err != nil {
		return WeKnoraAnswer{}, err
	}
	config, err := c.agentConfig(ctx, login)
	if err != nil {
		return WeKnoraAnswer{}, err
	}
	if c.config.KnowledgeBaseID != "" {
		config.KnowledgeBases = []string{c.config.KnowledgeBaseID}
	}
	if config.MaxIterations == 0 {
		config.MaxIterations = 8
	}
	if len(config.AllowedTools) == 0 {
		config.AllowedTools = []string{"knowledge_search"}
	}

	sessionID, err := c.createSession(ctx, login, config)
	if err != nil {
		return WeKnoraAnswer{}, err
	}
	if err := c.streamAgentAnswer(ctx, login, sessionID, scopedQuestion(question, scope), config.KnowledgeBases); err != nil {
		return WeKnoraAnswer{}, err
	}
	return c.loadAnswer(ctx, login, sessionID)
}

// scopedQuestion turns the page's retrieval mode into an explicit request for
// the WeKnora agent. The original question remains intact for display/audit.
func scopedQuestion(question, scope string) string {
	switch scope {
	case "仅内部":
		return "请仅使用机构内部研究记忆回答，不调用实时或联网数据。\n\n问题：" + question
	case "内部 + 实时":
		return "请结合机构内部研究记忆与必要的实时市场数据回答，并分别标注来源与时间。\n\n问题：" + question
	case "仅原始来源":
		return "请只使用实时数据、原始来源或联网检索回答，不以历史研究结论作为事实依据。\n\n问题：" + question
	default:
		return question
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

func (c *WeKnoraClient) streamAgentAnswer(ctx context.Context, login weKnoraLogin, sessionID, question string, knowledgeBases []string) error {
	body, err := json.Marshal(map[string]any{
		"query": question, "agent_enabled": true, "agent_id": c.config.AgentID,
		"knowledge_base_ids": knowledgeBases, "channel": "web",
	})
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
		answer := WeKnoraAnswer{Conclusion: message.Content}
		for _, reference := range message.References {
			title := firstNonEmpty(reference.KnowledgeTitle, reference.KnowledgeFile, reference.ID, reference.KnowledgeID)
			answer.Citations = append(answer.Citations, ResearchCitation{ID: firstNonEmpty(reference.ID, reference.KnowledgeID), Title: title, URL: reference.Metadata.URL})
		}
		return answer, nil
	}
	return WeKnoraAnswer{}, fmt.Errorf("WeKnora returned no completed assistant answer")
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
