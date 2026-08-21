package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	memoryScopeInternal     = "internal"
	memoryScopeInternalLive = "internal_live"
)

var (
	errMemoryAgentNotConfigured          = errors.New("research memory agent is not configured")
	errMemoryAgentDirectoryNotConfigured = errors.New("research memory agent directory is not configured")
	errInvalidMemorySession              = errors.New("invalid research memory session")
)

type memoryAgentService struct {
	client *WeKnoraClient
	codec  memorySessionCodec
}

type memorySessionCodec struct{ aead cipher.AEAD }

type memoryAgentCapability struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type memoryAgentInfo struct {
	Name         string                  `json:"name"`
	Description  string                  `json:"description"`
	Avatar       string                  `json:"avatar"`
	Capabilities []memoryAgentCapability `json:"capabilities"`
}

// memoryAgentDirectoryItem is the safe browser-facing representation of a
// WeKnora platform agent. It intentionally omits config, tenant, owner and
// timestamp fields returned by the upstream API.
type memoryAgentDirectoryItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Avatar      string `json:"avatar"`
	IsBuiltin   bool   `json:"is_builtin"`
}

type memorySessionRequest struct {
	Question string `json:"question"`
	Scope    string `json:"scope"`
}

type memorySessionMessage struct {
	Role    string         `json:"role"`
	Content string         `json:"content"`
	Answer  *WeKnoraAnswer `json:"answer,omitempty"`
}

func newMemoryAgentService(client *WeKnoraClient) *memoryAgentService {
	secret := client.config.MemorySessionSecret
	if secret == "" {
		secret = client.config.Password
	}
	return &memoryAgentService{client: client, codec: newMemorySessionCodec(secret)}
}

func newMemorySessionCodec(secret string) memorySessionCodec {
	key := sha256.Sum256([]byte("research-os-memory-session:" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		panic(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return memorySessionCodec{aead: aead}
}

func (c memorySessionCodec) seal(sessionID string) (string, error) {
	if strings.TrimSpace(sessionID) == "" {
		return "", errInvalidMemorySession
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := c.aead.Seal(nil, nonce, []byte(sessionID), nil)
	return base64.RawURLEncoding.EncodeToString(append(nonce, ciphertext...)), nil
}

func (c memorySessionCodec) open(token string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) <= c.aead.NonceSize() {
		return "", errInvalidMemorySession
	}
	plain, err := c.aead.Open(nil, raw[:c.aead.NonceSize()], raw[c.aead.NonceSize():], nil)
	if err != nil || strings.TrimSpace(string(plain)) == "" {
		return "", errInvalidMemorySession
	}
	return string(plain), nil
}

func memoryScope(scope string) (researchScope, error) {
	switch scope {
	case memoryScopeInternal:
		return researchScope{Label: "仅内部资料", UseInternal: true}, nil
	case memoryScopeInternalLive:
		return researchScope{Label: "内部 + 实时网页", UseInternal: true, UseExternalLive: true}, nil
	default:
		return researchScope{}, fmt.Errorf("unsupported memory scope %q", scope)
	}
}

func (s *memoryAgentService) enabled() bool {
	return s != nil && s.client != nil && s.client.config.memoryAgentEnabled()
}

func (s *memoryAgentService) directoryEnabled() bool {
	return s != nil && s.client != nil && s.client.config.BaseURL != "" && s.client.config.Email != "" && s.client.config.Password != ""
}

func (s *memoryAgentService) listDirectory(ctx context.Context) ([]memoryAgentDirectoryItem, string, error) {
	if !s.directoryEnabled() {
		return nil, "", errMemoryAgentDirectoryNotConfigured
	}
	login, err := s.client.login(ctx)
	if err != nil {
		return nil, "", err
	}
	agents, err := s.client.listAgents(ctx, login)
	if err != nil {
		return nil, "", err
	}
	items := make([]memoryAgentDirectoryItem, 0, len(agents))
	for _, agent := range agents {
		if strings.TrimSpace(agent.ID) == "" || strings.TrimSpace(agent.Name) == "" {
			continue
		}
		items = append(items, memoryAgentDirectoryItem{
			ID:          agent.ID,
			Name:        agent.Name,
			Description: agent.Description,
			Avatar:      agent.Avatar,
			IsBuiltin:   agent.IsBuiltin,
		})
	}
	return items, strings.TrimRight(s.client.config.consoleURL(), "/") + "/platform/agents", nil
}

func (s *memoryAgentService) agentInfo(ctx context.Context) (memoryAgentInfo, error) {
	if !s.enabled() {
		return memoryAgentInfo{}, errMemoryAgentNotConfigured
	}
	login, err := s.client.login(ctx)
	if err != nil {
		return memoryAgentInfo{}, err
	}
	detail, err := s.client.agentDetail(ctx, login, s.client.config.MemoryAgentID)
	if err != nil {
		return memoryAgentInfo{}, err
	}
	config := detail.Data.Config
	return memoryAgentInfo{
		Name:        firstNonEmpty(detail.Data.Name, "HYGR 研究记忆"),
		Description: firstNonEmpty(detail.Data.Description, "基于机构知识库沉淀、追溯与验证研究判断。"),
		Avatar:      firstNonEmpty(detail.Data.Avatar, "◈"),
		Capabilities: []memoryAgentCapability{
			{ID: "memory", Title: "研究记忆检索", Description: "从已授权的机构研究材料中定位结论与上下文。", Enabled: len(config.KnowledgeBases) > 0},
			{ID: "trace", Title: "观点追溯", Description: "结合多轮上下文追溯报告、命题与时间锚点。", Enabled: config.MultiTurnEnabled && config.RetainRetrievalHistory},
			{ID: "evidence", Title: "证据定位", Description: "回答附带可展示的知识库引用与检索证据。", Enabled: config.CitationEnabled},
			{ID: "live", Title: "实时资料补充", Description: "在每次明确选择后，补充外部实时网页信息。", Enabled: config.WebSearchEnabled},
		},
	}, nil
}

func (s *memoryAgentService) createSession(ctx context.Context) (string, error) {
	if !s.enabled() {
		return "", errMemoryAgentNotConfigured
	}
	login, err := s.client.login(ctx)
	if err != nil {
		return "", err
	}
	detail, err := s.client.agentDetail(ctx, login, s.client.config.MemoryAgentID)
	if err != nil {
		return "", err
	}
	config := detail.Data.Config
	config.KBSelectionMode = "selected"
	if s.client.config.KnowledgeBaseID != "" {
		config.KnowledgeBases = []string{s.client.config.KnowledgeBaseID}
	}
	if config.MaxIterations == 0 {
		config.MaxIterations = 8
	}
	if len(config.AllowedTools) == 0 {
		config.AllowedTools = []string{"knowledge_search"}
	}
	config.MultiTurnEnabled = true
	config.RetainRetrievalHistory = true
	config.WebSearchEnabled = true
	if config.HistoryTurns == 0 {
		config.HistoryTurns = 5
	}
	return s.client.createSession(ctx, login, config)
}

func (s *memoryAgentService) ask(ctx context.Context, sessionID, question, scope string) (WeKnoraAnswer, researchScope, error) {
	if !s.enabled() {
		return WeKnoraAnswer{}, researchScope{}, errMemoryAgentNotConfigured
	}
	policy, err := memoryScope(scope)
	if err != nil {
		return WeKnoraAnswer{}, researchScope{}, err
	}
	if strings.TrimSpace(question) == "" {
		return WeKnoraAnswer{}, policy, errors.New("question is required")
	}
	login, err := s.client.login(ctx)
	if err != nil {
		return WeKnoraAnswer{}, policy, err
	}
	knowledgeBases := []string{}
	if s.client.config.KnowledgeBaseID != "" {
		knowledgeBases = []string{s.client.config.KnowledgeBaseID}
	}
	if err := s.client.streamAgentAnswerFor(ctx, login, s.client.config.MemoryAgentID, sessionID, strings.TrimSpace(question), knowledgeBases, policy.UseExternalLive); err != nil {
		return WeKnoraAnswer{}, policy, err
	}
	answer, err := s.client.loadAnswer(ctx, login, sessionID)
	return answer, policy, err
}

func (s *memoryAgentService) loadMessages(ctx context.Context, sessionID string) ([]memorySessionMessage, error) {
	if !s.enabled() {
		return nil, errMemoryAgentNotConfigured
	}
	login, err := s.client.login(ctx)
	if err != nil {
		return nil, err
	}
	var response messagesResponse
	if err := s.client.json(ctx, http.MethodGet, "/api/v1/messages/"+sessionID+"/load?limit=50", &login, nil, &response); err != nil {
		return nil, err
	}
	messages := make([]memorySessionMessage, 0, len(response.Data))
	for _, message := range response.Data {
		if message.Role == "assistant" && !message.Completed {
			continue
		}
		item := memorySessionMessage{Role: message.Role, Content: message.Content}
		if message.Role == "assistant" {
			answer, ok := answerFromStoredMessage(message)
			if !ok {
				continue
			}
			item.Content = answer.Conclusion
			item.Answer = &answer
		}
		if item.Role == "user" || item.Role == "assistant" {
			messages = append(messages, item)
		}
	}
	return messages, nil
}

func (s *memoryAgentService) serveInfo(w http.ResponseWriter, r *http.Request) {
	info, err := s.agentInfo(r.Context())
	if err != nil {
		writeMemoryAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent": info, "source": "weknora-memory-agent"})
}

func (s *memoryAgentService) serveDirectory(w http.ResponseWriter, r *http.Request) {
	agents, consoleURL, err := s.listDirectory(r.Context())
	if err != nil {
		writeMemoryAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"agents":      agents,
		"console_url": consoleURL,
		"source":      "weknora-agent-directory",
	})
}

func (s *memoryAgentService) serveCreateSession(w http.ResponseWriter, r *http.Request) {
	sessionID, err := s.createSession(r.Context())
	if err != nil {
		writeMemoryAgentError(w, err)
		return
	}
	token, err := s.codec.seal(sessionID)
	if err != nil {
		writeMemoryAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"session_id": token})
}

func (s *memoryAgentService) serveMessages(w http.ResponseWriter, r *http.Request) {
	sessionID, err := s.codec.open(r.PathValue("session"))
	if err != nil {
		writeMemoryAgentError(w, err)
		return
	}
	messages, err := s.loadMessages(r.Context(), sessionID)
	if err != nil {
		writeMemoryAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": messages})
}

func (s *memoryAgentService) serveAsk(w http.ResponseWriter, r *http.Request) {
	sessionID, err := s.codec.open(r.PathValue("session"))
	if err != nil {
		writeMemoryAgentError(w, err)
		return
	}
	var request memorySessionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil || strings.TrimSpace(request.Question) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "question is required"})
		return
	}
	answer, policy, err := s.ask(r.Context(), sessionID, request.Question, request.Scope)
	if err != nil {
		writeMemoryAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scope": policy.Label, "answer": answer, "source": "weknora-memory-agent"})
}

func writeMemoryAgentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errMemoryAgentDirectoryNotConfigured):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "WeKnora 智能体目录未连接，请联系管理员完成配置"})
	case errors.Is(err, errMemoryAgentNotConfigured):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "研究记忆智能体未连接，请联系管理员完成配置"})
	case errors.Is(err, errInvalidMemorySession):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "研究记忆会话无效，请新建会话后重试"})
	case strings.Contains(err.Error(), "unsupported memory scope"):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scope must be internal or internal_live"})
	case strings.Contains(err.Error(), "question is required"):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "question is required"})
	default:
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "研究记忆智能体暂时无法响应，请稍后重试"})
	}
}
