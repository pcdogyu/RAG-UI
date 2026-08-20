package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const watchlistBriefCacheTTL = 15 * time.Minute
const watchlistBriefMaxNews = 10

const watchlistBriefPrompt = `你是机构研究工作台的实时市场简报编辑。只使用刚刚检索到的外部实时资料、新闻原始来源与已授权外部工具；严禁调用、引用或基于任何内部知识库内容推断。

请围绕 Crypto、美国股票、宏观与两者相关的重大事件，生成中文简报。严格只返回一个 JSON 对象，不要 Markdown、代码围栏、解释或额外字段。结构必须为：
{
  "crypto":[{"label":"Bull","content":"..."},{"label":"Base","content":"..."},{"label":"Bear","content":"..."}],
  "us_equities":[{"label":"Bull","content":"..."},{"label":"Base","content":"..."},{"label":"Bear","content":"..."}],
  "news":[{"title":"...","summary":"...","source":"发布者名称","published_at":"2026-08-20 10:30 UTC","url":"https://..."}]
}
要求：
1. 每个 Bull/Base/Bear 情景必须具体说明触发条件或观察点，且不构成投资建议。
2. news 必须返回恰好 10 条重大、最新且与 Crypto、美股或宏观传导相关的新闻；按影响与新近程度排序。若无法验证 10 条不同新闻，请明确说明无法完成，不要用占位或虚构内容补齐。
3. 每条 news 必须有 source 和 published_at；只有确认的 http/https 原文链接才填写 url，否则 url 留空。
4. 不得编造数字、新闻、链接或发布时间；无法确认的信息不应写入。`

type watchlistScenario struct {
	Label   string `json:"label"`
	Content string `json:"content"`
}

type watchlistNews struct {
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	Source      string `json:"source"`
	PublishedAt string `json:"published_at"`
	URL         string `json:"url,omitempty"`
}

type watchlistBrief struct {
	GeneratedAt time.Time           `json:"generated_at"`
	Crypto      []watchlistScenario `json:"crypto"`
	USEquities  []watchlistScenario `json:"us_equities"`
	News        []watchlistNews     `json:"news"`
	Cached      bool                `json:"cached"`
}

type watchlistBriefPayload struct {
	Crypto     []watchlistScenario `json:"crypto"`
	USEquities []watchlistScenario `json:"us_equities"`
	News       []watchlistNews     `json:"news"`
}

type watchlistBriefService struct {
	client     *WeKnoraClient
	mu         sync.Mutex
	cached     watchlistBrief
	cacheUntil time.Time
	refreshing chan struct{}
}

func newWatchlistBriefService(client *WeKnoraClient) *watchlistBriefService {
	return &watchlistBriefService{client: client}
}

func (s *watchlistBriefService) Brief(ctx context.Context, force bool) (watchlistBrief, error) {
	s.mu.Lock()
	if !force && !s.cached.GeneratedAt.IsZero() && time.Now().Before(s.cacheUntil) {
		brief := s.cached
		brief.Cached = true
		s.mu.Unlock()
		return brief, nil
	}
	if s.refreshing != nil {
		refreshing := s.refreshing
		s.mu.Unlock()
		select {
		case <-refreshing:
			s.mu.Lock()
			brief := s.cached
			hasBrief := !brief.GeneratedAt.IsZero()
			s.mu.Unlock()
			if hasBrief {
				brief.Cached = true
				return brief, nil
			}
			return watchlistBrief{}, errors.New("实时市场简报生成失败")
		case <-ctx.Done():
			return watchlistBrief{}, ctx.Err()
		}
	}
	refreshing := make(chan struct{})
	s.refreshing = refreshing
	s.mu.Unlock()

	brief, err := s.generate(ctx)
	s.mu.Lock()
	if err == nil {
		s.cached = brief
		s.cacheUntil = time.Now().Add(watchlistBriefCacheTTL)
	}
	s.refreshing = nil
	close(refreshing)
	s.mu.Unlock()
	return brief, err
}

func (s *watchlistBriefService) generate(ctx context.Context) (watchlistBrief, error) {
	answer, err := s.client.Ask(ctx, watchlistBriefPrompt, "实时")
	if err != nil {
		return watchlistBrief{}, err
	}
	payload, err := parseWatchlistBrief(answer.Conclusion)
	if err != nil {
		return watchlistBrief{}, fmt.Errorf("invalid WeKnora market brief: %w", err)
	}
	return watchlistBrief{GeneratedAt: time.Now().UTC(), Crypto: payload.Crypto, USEquities: payload.USEquities, News: payload.News}, nil
}

func parseWatchlistBrief(content string) (watchlistBriefPayload, error) {
	start, end := strings.Index(content, "{"), strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return watchlistBriefPayload{}, errors.New("未找到 JSON 对象")
	}
	var payload watchlistBriefPayload
	decoder := json.NewDecoder(strings.NewReader(content[start : end+1]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return watchlistBriefPayload{}, err
	}
	if err := validateWatchlistScenarios("crypto", payload.Crypto); err != nil {
		return watchlistBriefPayload{}, err
	}
	if err := validateWatchlistScenarios("us_equities", payload.USEquities); err != nil {
		return watchlistBriefPayload{}, err
	}
	if len(payload.News) != watchlistBriefMaxNews {
		return watchlistBriefPayload{}, fmt.Errorf("重大新闻数量必须为 %d 条", watchlistBriefMaxNews)
	}
	for index := range payload.News {
		item := &payload.News[index]
		item.Title = strings.TrimSpace(item.Title)
		item.Summary = strings.TrimSpace(item.Summary)
		item.Source = strings.TrimSpace(item.Source)
		item.PublishedAt = strings.TrimSpace(item.PublishedAt)
		item.URL = safeExternalURL(item.URL)
		if item.Title == "" || item.Summary == "" || item.Source == "" || item.PublishedAt == "" {
			return watchlistBriefPayload{}, fmt.Errorf("第 %d 条重大新闻缺少必要字段", index+1)
		}
	}
	return payload, nil
}

func validateWatchlistScenarios(name string, scenarios []watchlistScenario) error {
	if len(scenarios) != 3 {
		return fmt.Errorf("%s 必须包含 3 条情景", name)
	}
	wanted := []string{"Bull", "Base", "Bear"}
	byLabel := make(map[string]watchlistScenario, 3)
	for _, scenario := range scenarios {
		label := strings.ToLower(strings.TrimSpace(scenario.Label))
		content := strings.TrimSpace(scenario.Content)
		if content == "" {
			return fmt.Errorf("%s 情景内容不能为空", name)
		}
		if _, exists := byLabel[label]; exists {
			return fmt.Errorf("%s 存在重复情景标签", name)
		}
		byLabel[label] = watchlistScenario{Label: strings.Title(label), Content: content}
	}
	normalized := make([]watchlistScenario, 0, 3)
	for _, label := range wanted {
		scenario, ok := byLabel[strings.ToLower(label)]
		if !ok {
			return fmt.Errorf("%s 缺少 %s 情景", name, label)
		}
		normalized = append(normalized, scenario)
	}
	copy(scenarios, normalized)
	return nil
}

func safeExternalURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return parsed.String()
}

func (s *watchlistBriefService) serveBrief(w http.ResponseWriter, r *http.Request) {
	refresh := r.URL.Query().Get("refresh")
	force := refresh == "1" || strings.EqualFold(refresh, "true")
	if refresh != "" && !force {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "refresh must be 1 or true"})
		return
	}
	if !s.client.config.enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "WeKnora 实时检索尚未配置"})
		return
	}
	brief, err := s.Brief(r.Context(), force)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "暂时无法生成实时市场简报，请稍后刷新"})
		return
	}
	writeJSON(w, http.StatusOK, brief)
}
