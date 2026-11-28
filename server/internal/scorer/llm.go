package scorer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"intel-hub/internal/config"
	"intel-hub/internal/model"
)

// Client calls OpenAI-compatible chat completion APIs with runtime-switchable 3-slot models
// completely isolated from Hermes's own chat model configuration.
type Client struct {
	mu           sync.RWMutex
	cfg          config.LLMConfig
	httpClient   *http.Client
	usageHook    func(model.LLMUsageRecord)
	quotaMu      sync.Mutex
	quotaCache   *model.SubscriptionQuota
	quotaCacheAt time.Time
}

func NewClient(cfg config.LLMConfig) *Client {
	if cfg.Provider == "" {
		cfg.Provider = "opencode-go"
	}
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 25 * time.Second
	}
	return &Client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// SetUsageHook registers a persistence callback invoked after each LLM call.
func (c *Client) SetUsageHook(fn func(model.LLMUsageRecord)) {
	c.mu.Lock()
	c.usageHook = fn
	c.mu.Unlock()
}

func (c *Client) emitUsage(rec model.LLMUsageRecord) {
	c.mu.RLock()
	fn := c.usageHook
	c.mu.RUnlock()
	if fn != nil {
		fn(rec)
	}
}

// FetchSubscriptionQuota queries OpenCode Go's live rolling/weekly/monthly usage with a 60s cache.
func (c *Client) FetchSubscriptionQuota(ctx context.Context, forceRefresh bool) *model.SubscriptionQuota {
	apiKey, endpoint := c.resolveOpenCodeGoQuotaEndpoint()
	if apiKey == "" || endpoint == "" {
		return nil
	}

	c.quotaMu.Lock()
	defer c.quotaMu.Unlock()

	if !forceRefresh && c.quotaCache != nil && time.Since(c.quotaCacheAt) < 60*time.Second {
		cp := *c.quotaCache
		return &cp
	}

	qctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(qctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return c.quotaFallback("构建额度请求失败")
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return c.quotaFallback("查询订阅额度超时，请稍后刷新")
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if resp.StatusCode != http.StatusOK {
		return c.quotaFallback(fmt.Sprintf("订阅额度接口返回 HTTP %d", resp.StatusCode))
	}

	var raw struct {
		Usage struct {
			Rolling *struct {
				Status   string  `json:"status"`
				Percent  float64 `json:"percent"`
				ResetsAt string  `json:"resetsAt"`
			} `json:"rolling"`
			Weekly *struct {
				Status   string  `json:"status"`
				Percent  float64 `json:"percent"`
				ResetsAt string  `json:"resetsAt"`
			} `json:"weekly"`
			Monthly *struct {
				Status   string  `json:"status"`
				Percent  float64 `json:"percent"`
				ResetsAt string  `json:"resetsAt"`
			} `json:"monthly"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return c.quotaFallback("解析订阅额度响应失败")
	}

	toWindow := func(w *struct {
		Status   string  `json:"status"`
		Percent  float64 `json:"percent"`
		ResetsAt string  `json:"resetsAt"`
	}) *model.QuotaWindow {
		if w == nil {
			return nil
		}
		used := clampPercent(w.Percent)
		return &model.QuotaWindow{
			Status:           w.Status,
			UsedPercent:      used,
			RemainingPercent: clampPercent(100 - used),
			ResetsAt:         w.ResetsAt,
		}
	}

	q := &model.SubscriptionQuota{
		Provider:  "opencode-go",
		Available: raw.Usage.Rolling != nil || raw.Usage.Weekly != nil || raw.Usage.Monthly != nil,
		FetchedAt: time.Now().Unix(),
		Rolling:   toWindow(raw.Usage.Rolling),
		Weekly:    toWindow(raw.Usage.Weekly),
		Monthly:   toWindow(raw.Usage.Monthly),
	}
	if !q.Available {
		return c.quotaFallback("订阅额度字段缺失")
	}
	c.quotaCache = q
	c.quotaCacheAt = time.Now()
	cp := *q
	return &cp
}

func (c *Client) quotaFallback(errMsg string) *model.SubscriptionQuota {
	if c.quotaCache != nil && time.Since(c.quotaCacheAt) < 10*time.Minute {
		cp := *c.quotaCache
		cp.Error = errMsg
		return &cp
	}
	return &model.SubscriptionQuota{
		Provider:  "opencode-go",
		Available: false,
		FetchedAt: time.Now().Unix(),
		Error:     errMsg,
	}
}

func (c *Client) resolveOpenCodeGoQuotaEndpoint() (string, string) {
	c.mu.RLock()
	provider := strings.ToLower(strings.TrimSpace(c.cfg.Provider))
	baseURL := strings.TrimSpace(c.cfg.BaseURL)
	apiKey := strings.TrimSpace(c.cfg.APIKey)
	presets := append([]config.LLMProviderPreset(nil), c.cfg.Providers...)
	c.mu.RUnlock()

	if (provider == "opencode-go" || strings.Contains(strings.ToLower(baseURL), "opencode.ai/zen/go")) && apiKey != "" {
		return apiKey, strings.TrimRight(baseURL, "/") + "/usage"
	}
	for _, p := range presets {
		if strings.EqualFold(p.Name, "opencode-go") || strings.Contains(strings.ToLower(p.BaseURL), "opencode.ai/zen/go") {
			k := strings.TrimSpace(p.APIKey)
			if k == "" && p.APIKeyEnv != "" {
				k = strings.TrimSpace(os.Getenv(p.APIKeyEnv))
			}
			if k != "" {
				b := strings.TrimSpace(p.BaseURL)
				if b == "" {
					b = "https://opencode.ai/zen/go/v1"
				}
				return k, strings.TrimRight(b, "/") + "/usage"
			}
		}
	}
	if k := strings.TrimSpace(os.Getenv("OPENCODE_GO_API_KEY")); k != "" {
		return k, "https://opencode.ai/zen/go/v1/usage"
	}
	return "", ""
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// GetSelection returns the currently active 3-slot model configuration.
func (c *Client) GetSelection() model.ModelSelection {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return model.ModelSelection{
		Provider:      c.cfg.Provider,
		FastModel:     c.cfg.FastModel,
		FallbackModel: c.cfg.FallbackModel,
		StrongModel:   c.cfg.StrongModel,
	}
}

// UpdateSelection hot-swaps the active provider and 3 model slots in memory (zero restart, zero impact on Hermes).
func (c *Client) UpdateSelection(sel model.ModelSelection) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(sel.Provider) != "" {
		c.cfg.Provider = strings.TrimSpace(sel.Provider)
		for _, p := range c.cfg.Providers {
			if p.Name == c.cfg.Provider {
				if p.BaseURL != "" {
					c.cfg.BaseURL = p.BaseURL
				}
				if p.APIKey != "" {
					c.cfg.APIKey = p.APIKey
				} else if p.APIKeyEnv != "" {
					if v := strings.TrimSpace(os.Getenv(p.APIKeyEnv)); v != "" {
						c.cfg.APIKey = v
					}
				}
				break
			}
		}
	}
	if strings.TrimSpace(sel.FastModel) != "" {
		c.cfg.FastModel = strings.TrimSpace(sel.FastModel)
	}
	if strings.TrimSpace(sel.FallbackModel) != "" {
		c.cfg.FallbackModel = strings.TrimSpace(sel.FallbackModel)
	}
	if strings.TrimSpace(sel.StrongModel) != "" {
		c.cfg.StrongModel = strings.TrimSpace(sel.StrongModel)
	}
}

// ListProviders reads ~/.hermes/provider_models_cache.json (read-only) and merges any custom YAML presets.
func (c *Client) ListProviders() []model.ModelProviderCatalog {
	c.mu.RLock()
	presets := append([]config.LLMProviderPreset(nil), c.cfg.Providers...)
	activeProvider := c.cfg.Provider
	activeBase := c.cfg.BaseURL
	activeModels := []string{c.cfg.FastModel, c.cfg.FallbackModel, c.cfg.StrongModel}
	c.mu.RUnlock()

	byName := make(map[string]model.ModelProviderCatalog)

	// 1. Read-only discovery from Hermes provider_models_cache.json
	if home, err := os.UserHomeDir(); err == nil {
		cachePath := filepath.Join(home, ".hermes", "provider_models_cache.json")
		if raw, err := os.ReadFile(cachePath); err == nil {
			var cache map[string]struct {
				Models []string `json:"models"`
			}
			if err := json.Unmarshal(raw, &cache); err == nil {
				for provName, entry := range cache {
					if len(entry.Models) == 0 {
						continue
					}
					models := append([]string(nil), entry.Models...)
					sort.Strings(models)
					base := ""
					if provName == "opencode-go" {
						base = "https://opencode.ai/zen/go/v1"
					}
					byName[provName] = model.ModelProviderCatalog{
						Name:    provName,
						BaseURL: base,
						Models:  models,
					}
				}
			}
		}
	}

	// 2. Merge custom YAML provider presets
	for _, p := range presets {
		if p.Name == "" {
			continue
		}
		models := append([]string(nil), p.Models...)
		sort.Strings(models)
		byName[p.Name] = model.ModelProviderCatalog{
			Name:    p.Name,
			BaseURL: p.BaseURL,
			Models:  models,
		}
	}

	// 3. Ensure active provider always appears even on dev machines without Hermes cache
	if _, exists := byName[activeProvider]; !exists {
		seen := map[string]bool{}
		var fallbackList []string
		for _, m := range activeModels {
			if m != "" && !seen[m] {
				seen[m] = true
				fallbackList = append(fallbackList, m)
			}
		}
		byName[activeProvider] = model.ModelProviderCatalog{
			Name:    activeProvider,
			BaseURL: activeBase,
			Models:  fallbackList,
		}
	}

	out := make([]model.ModelProviderCatalog, 0, len(byName))
	for _, cat := range byName {
		out = append(out, cat)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

type streamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatReq struct {
	Model         string      `json:"model"`
	Messages      []chatMsg   `json:"messages"`
	Temperature   float64     `json:"temperature"`
	Stream        bool        `json:"stream,omitempty"`
	StreamOptions *streamOpts `json:"stream_options,omitempty"`
}

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type usageStats struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type chatResp struct {
	Choices []struct {
		Message chatMsg `json:"message"`
	} `json:"choices"`
	Usage usageStats `json:"usage"`
}

// ScoreEvent runs L4 fast AI judge scoring. Falls back to heuristic scoring if LLM API key is absent.
func (c *Client) ScoreEvent(ctx context.Context, item model.RawItem, extraContext string) (model.ScoreResult, int64, error) {
	c.mu.RLock()
	provider := c.cfg.Provider
	apiKey := c.cfg.APIKey
	baseURL := c.cfg.BaseURL
	fastModel := c.cfg.FastModel
	fallbackModel := c.cfg.FallbackModel
	c.mu.RUnlock()

	if strings.TrimSpace(apiKey) == "" {
		return heuristicScore(item), 0, nil
	}

	sysPrompt := `你是全天候金融与科技突发情报裁判。请评估以下消息的突发紧迫度与行业影响，严格仅输出一个 JSON 对象，不要包含 Markdown 代码块或任何多余文字：
{"score":1-10整数,"category":"finance|ai|bigtech|world","zh_title":"18字内高密度简体中文标题","impact":"60字内核心事实与市场/行业影响推演","entities":["大写英文代码或中文实体名"],"primary_topic_key":"category:核心实体大写","follow_ups":["3条紧扣本文具体事实的深度追问，每条14-24字，直接围绕文中主体/数据/因果，禁止套用宏观/原油/外交等无关模板"]}
评分标准：
9-10分(P1特急)：重大黑天鹅、美股核心标的剧烈异动(>4%)、美联储意外决议、旗舰基座模型发布/开源、主力AI API大面积宕机、百亿美元并购/反垄断重罚、影响全球供应链的重大冲突或0-day。
8分(P1重要)：明确高价值突发新闻、重要高管变动、大厂战略转向、核心芯片/AI重要进展。
5-7分(P2次级)：有价值的行业动态、财报常规解读、普通产品更新，适合归入晚间日报。
1-4分(噪音)：观点水文、软广、无实质增量的常规报道；以及缺乏明确新闻事实与上下文背景的 X/Twitter 简短回复、表情或泛泛感叹（哪怕出自马斯克等名人，无实质增量也必须压低至 1-3 分）。`

	userText := fmt.Sprintf("信源: %s (%s)\n提示领域: %s\n标题: %s\n摘要: %s",
		item.SourceName, item.SourceType, item.CategoryHint, item.Title, truncateRunes(item.Summary, 600))
	if extraContext != "" {
		userText += "\n附加实时检索上下文:\n" + truncateRunes(extraContext, 600)
	}

	models := []string{fastModel}
	if fallbackModel != "" && fallbackModel != fastModel {
		models = append(models, fallbackModel)
	}

	var lastErr error
	for _, mdl := range models {
		if mdl == "" {
			continue
		}
		msgs := []chatMsg{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: userText},
		}
		content, usage, estimated, err := c.completeOnce(ctx, baseURL, apiKey, mdl, "intel-hub-scorer", msgs, 0.1)
		if err != nil {
			lastErr = err
			continue
		}
		c.emitUsage(model.LLMUsageRecord{
			Provider:         provider,
			Model:            mdl,
			Scene:            "scoring",
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TotalTokens:      usage.TotalTokens,
			Estimated:        estimated,
		})
		res, err := parseScoreJSON(content)
		if err != nil {
			lastErr = err
			continue
		}
		if res.Score < 1 {
			res.Score = 1
		}
		if res.Score > 10 {
			res.Score = 10
		}
		if res.Category == "" {
			res.Category = normalizeCategory(item.CategoryHint)
		}
		if res.ZhTitle == "" {
			res.ZhTitle = truncateRunes(item.Title, 24)
		}
		res.FollowUps = sanitizeFollowUps(res.FollowUps)
		return res, usage.TotalTokens, nil
	}

	return heuristicScore(item), 0, fmt.Errorf("llm scoring fallback used: %w", lastErr)
}

// StreamAsk streams a deep analysis answer under the default ask_event scene.
func (c *Client) StreamAsk(ctx context.Context, systemPrompt, userPrompt string, history []model.EventThread, onDelta func(delta string) error) (fullText string, modelUsed string, err error) {
	return c.StreamAskWithScene(ctx, "ask_event", systemPrompt, userPrompt, history, onDelta)
}

// StreamAskWithScene streams a deep analysis answer (SSE token deltas) with automatic transient retry,
// graceful model fallback (strong -> fallback -> fast), and token accounting tagged by scene.
func (c *Client) StreamAskWithScene(ctx context.Context, scene, systemPrompt, userPrompt string, history []model.EventThread, onDelta func(delta string) error) (fullText string, modelUsed string, err error) {
	c.mu.RLock()
	provider := c.cfg.Provider
	apiKey := c.cfg.APIKey
	baseURL := c.cfg.BaseURL
	strongModel := strings.TrimSpace(c.cfg.StrongModel)
	fallbackModel := strings.TrimSpace(c.cfg.FallbackModel)
	fastModel := strings.TrimSpace(c.cfg.FastModel)
	c.mu.RUnlock()

	if strings.TrimSpace(apiKey) == "" {
		msg := "当前服务端尚未配置 `OPENCODE_GO_API_KEY` / `INTEL_LLM_API_KEY`，无法调用流式大模型深度分析。请在 `~/.hermes/.env` 配置后重试。"
		_ = onDelta(msg)
		return msg, "none", nil
	}

	// Build deduplicated model fallback chain: strong -> fallback -> fast
	seen := map[string]bool{}
	var candidates []string
	for _, m := range []string{strongModel, fallbackModel, fastModel} {
		if m != "" && !seen[m] {
			seen[m] = true
			candidates = append(candidates, m)
		}
	}
	if len(candidates) == 0 {
		return "", "", fmt.Errorf("no LLM model configured")
	}

	msgs := []chatMsg{{Role: "system", Content: systemPrompt}}
	for _, h := range history {
		msgs = append(msgs, chatMsg{Role: h.Role, Content: h.ContentMD})
	}
	msgs = append(msgs, chatMsg{Role: "user", Content: userPrompt})

	// Dedicated streaming client: 45s cap waiting for response headers, unlimited total stream duration once flowing.
	streamClient := &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 45 * time.Second,
			ExpectContinueTimeout: 2 * time.Second,
			ForceAttemptHTTP2:     true,
		},
	}

	recordStreamUsage := func(mdl, output string, u usageStats) {
		estimated := false
		if u.TotalTokens <= 0 && u.PromptTokens <= 0 && u.CompletionTokens <= 0 {
			u.PromptTokens = estimateMessagesTokens(msgs)
			u.CompletionTokens = estimateTextTokens(output)
			u.TotalTokens = u.PromptTokens + u.CompletionTokens
			estimated = true
		} else if u.TotalTokens <= 0 {
			u.TotalTokens = u.PromptTokens + u.CompletionTokens
		}
		if u.TotalTokens > 0 {
			c.emitUsage(model.LLMUsageRecord{
				Provider:         provider,
				Model:            mdl,
				Scene:            scene,
				PromptTokens:     u.PromptTokens,
				CompletionTokens: u.CompletionTokens,
				TotalTokens:      u.TotalTokens,
				Estimated:        estimated,
			})
		}
	}

	var lastErr error
	for idx, mdl := range candidates {
		// Retry primary model once on transient failure before falling back
		maxAttempts := 1
		if idx == 0 {
			maxAttempts = 2
		}
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			if ctx.Err() != nil {
				return "", mdl, ctx.Err()
			}
			emitted := false
			text, usage, streamErr := c.streamOnce(ctx, streamClient, baseURL, apiKey, mdl, msgs, true, func(d string) error {
				emitted = true
				return onDelta(d)
			})
			// Fallback without stream_options if proxy rejects the field before any tokens flow
			if streamErr != nil && !emitted && (strings.Contains(streamErr.Error(), "status 400") || strings.Contains(streamErr.Error(), "status 422")) {
				text, usage, streamErr = c.streamOnce(ctx, streamClient, baseURL, apiKey, mdl, msgs, false, func(d string) error {
					emitted = true
					return onDelta(d)
				})
			}
			if streamErr == nil && strings.TrimSpace(text) != "" {
				recordStreamUsage(mdl, text, usage)
				return text, mdl, nil
			}
			if emitted {
				recordStreamUsage(mdl, text, usage)
				return text, mdl, streamErr
			}
			if streamErr != nil {
				lastErr = streamErr
			} else {
				lastErr = fmt.Errorf("model %s returned empty stream", mdl)
			}
			if attempt < maxAttempts {
				select {
				case <-ctx.Done():
					return "", mdl, ctx.Err()
				case <-time.After(1200 * time.Millisecond):
				}
			}
		}
	}
	return "", candidates[0], lastErr
}

func (c *Client) streamOnce(ctx context.Context, streamClient *http.Client, baseURL, apiKey, modelName string, msgs []chatMsg, includeUsage bool, onDelta func(delta string) error) (string, usageStats, error) {
	payload := chatReq{
		Model:       modelName,
		Messages:    msgs,
		Temperature: 0.3,
		Stream:      true,
	}
	if includeUsage {
		payload.StreamOptions = &streamOpts{IncludeUsage: true}
	}
	body, _ := json.Marshal(payload)

	endpoint := strings.TrimRight(baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", usageStats{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if strings.Contains(strings.ToLower(baseURL), "opencode.ai") {
		req.Header.Set("x-opencode-session", fmt.Sprintf("infohub-ask-%d", time.Now().UnixNano()))
	}

	resp, err := streamClient.Do(req)
	if err != nil {
		return "", usageStats{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", usageStats{}, fmt.Errorf("llm stream status %d (%s): %s", resp.StatusCode, modelName, strings.TrimSpace(string(errBody)))
	}

	var full strings.Builder
	var usage usageStats
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		dataStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if dataStr == "" || dataStr == "[DONE]" {
			if dataStr == "[DONE]" {
				break
			}
			continue
		}
		var chunk struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			Usage   *usageStats `json:"usage"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(dataStr), &chunk); err == nil {
			if chunk.Error != nil && chunk.Error.Message != "" {
				return full.String(), usage, fmt.Errorf("upstream error (%s): %s", modelName, chunk.Error.Message)
			}
			if chunk.Usage != nil && (chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0) {
				usage = *chunk.Usage
			}
			if len(chunk.Choices) > 0 {
				delta := chunk.Choices[0].Delta.Content
				if delta != "" {
					full.WriteString(delta)
					if err := onDelta(delta); err != nil {
						return full.String(), usage, err
					}
				}
			}
		}
	}
	return full.String(), usage, sc.Err()
}

// GenerateDailyDigest synthesizes a structured Chinese Markdown executive briefing from pending P2 + P1 items.
func (c *Client) GenerateDailyDigest(ctx context.Context, dateStr string, p1Events []model.IntelEvent, p2Items []model.DigestItem) (string, string, error) {
	c.mu.RLock()
	provider := c.cfg.Provider
	apiKey := c.cfg.APIKey
	baseURL := c.cfg.BaseURL
	mdl := c.cfg.StrongModel
	if mdl == "" {
		mdl = c.cfg.FastModel
	}
	c.mu.RUnlock()

	if len(p1Events) == 0 && len(p2Items) == 0 {
		return "", mdl, fmt.Errorf("no events available for digest")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("日期：%s (UTC+8)\n\n", dateStr))
	if len(p1Events) > 0 {
		sb.WriteString("【今日 P1 高优突发（8-10分）】\n")
		maxP1 := 20
		if len(p1Events) < maxP1 {
			maxP1 = len(p1Events)
		}
		for _, ev := range p1Events[:maxP1] {
			sb.WriteString(fmt.Sprintf("- [%s | %d分 | %s] %s — %s (%s)\n",
				ev.EventCode, ev.Score, ev.Category, ev.ZhTitle, ev.ImpactAnalysis, ev.CanonicalURL))
		}
		sb.WriteString("\n")
	}
	if len(p2Items) > 0 {
		sb.WriteString("【今日 P2 次级重点动态（5-7分）】\n")
		maxP2 := 45
		if len(p2Items) < maxP2 {
			maxP2 = len(p2Items)
		}
		for _, it := range p2Items[:maxP2] {
			sb.WriteString(fmt.Sprintf("- [%s | %d分 | %s] %s (%s)\n",
				it.EventCode, it.Score, it.Category, it.BulletPoint, it.URL))
		}
	}

	if strings.TrimSpace(apiKey) == "" {
		return sb.String(), "raw", nil
	}

	sysPrompt := `你是全天候金融与科技资深情报主编。请将今日收集的 P1 突发与 P2 次级动态，去重合并、提炼跨事件因果脉络，编排为一份高信息密度、结构清晰的《今日情报内参》（Markdown 格式）。
要求：
1. 按「## 一、核心突发复盘」「## 二、金融与美股主线」「## 三、AI 与大模型演进」「## 四、大厂商业与供应链」「## 五、全球宏观与风险预警」「## 六、明日关注重点」组织（无内容的板块可省略）；
2. 禁止流水账罗列，合并同主题多篇报道，每条提炼关键事实+核心影响，并保留关键事件编号（如 [#EVT-...]）；
3. 语言简练专业，适合 3 分钟快速通读。`

	body, _ := json.Marshal(chatReq{
		Model:       mdl,
		Messages:    []chatMsg{{Role: "system", Content: sysPrompt}, {Role: "user", Content: sb.String()}},
		Temperature: 0.25,
	})
	endpoint := strings.TrimRight(baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", mdl, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if strings.Contains(strings.ToLower(baseURL), "opencode.ai") {
		req.Header.Set("x-opencode-session", "intel-hub-digest-"+dateStr)
	}
	longClient := &http.Client{Timeout: 110 * time.Second}
	resp, err := longClient.Do(req)
	if err != nil {
		return "", mdl, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode >= 300 {
		return "", mdl, fmt.Errorf("digest status %d: %s", resp.StatusCode, string(raw))
	}
	var parsed chatResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", mdl, err
	}
	if len(parsed.Choices) == 0 {
		return "", mdl, fmt.Errorf("empty choices for digest")
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	u := parsed.Usage
	est := false
	if u.TotalTokens <= 0 && u.PromptTokens <= 0 && u.CompletionTokens <= 0 {
		u.PromptTokens = estimateTextTokens(sysPrompt) + estimateTextTokens(sb.String())
		u.CompletionTokens = estimateTextTokens(content)
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
		est = true
	} else if u.TotalTokens <= 0 {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
	c.emitUsage(model.LLMUsageRecord{
		Provider:         provider,
		Model:            mdl,
		Scene:            "daily_digest",
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		Estimated:        est,
	})
	return content, mdl, nil
}

func (c *Client) completeOnce(ctx context.Context, baseURL, apiKey, modelName, sessionID string, msgs []chatMsg, temp float64) (string, usageStats, bool, error) {
	body, _ := json.Marshal(chatReq{
		Model:       modelName,
		Messages:    msgs,
		Temperature: temp,
	})
	endpoint := strings.TrimRight(baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", usageStats{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if strings.Contains(strings.ToLower(baseURL), "opencode.ai") {
		req.Header.Set("x-opencode-session", sessionID)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", usageStats{}, false, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode >= 300 {
		return "", usageStats{}, false, fmt.Errorf("status %d: %s", resp.StatusCode, string(raw))
	}
	var parsed chatResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", usageStats{}, false, err
	}
	if len(parsed.Choices) == 0 {
		return "", usageStats{}, false, fmt.Errorf("empty choices from model %s", modelName)
	}
	content := parsed.Choices[0].Message.Content
	u := parsed.Usage
	estimated := false
	if u.TotalTokens <= 0 && u.PromptTokens <= 0 && u.CompletionTokens <= 0 {
		u.PromptTokens = estimateMessagesTokens(msgs)
		u.CompletionTokens = estimateTextTokens(content)
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
		estimated = true
	} else if u.TotalTokens <= 0 {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
	return content, u, estimated, nil
}

func estimateMessagesTokens(msgs []chatMsg) int64 {
	var total int64
	for _, m := range msgs {
		total += 4 + estimateTextTokens(m.Content)
	}
	return total
}

func estimateTextTokens(s string) int64 {
	if s == "" {
		return 0
	}
	var cjk, ascii int64
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			cjk++
		} else {
			ascii++
		}
	}
	est := (cjk*2+2)/3 + (ascii+3)/4
	if est < 1 {
		est = 1
	}
	return est
}

func parseScoreJSON(raw string) (model.ScoreResult, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		raw = raw[start : end+1]
	}
	var res model.ScoreResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		return res, err
	}
	return res, nil
}

func heuristicScore(item model.RawItem) model.ScoreResult {
	score := 6
	if item.BypassRuleFilter {
		score = 8
	}
	lower := strings.ToLower(item.Title + " " + item.Summary)
	if strings.Contains(lower, "outage") || strings.Contains(lower, "宕机") ||
		strings.Contains(lower, "subpoena") || strings.Contains(lower, "熔断") ||
		strings.Contains(lower, "zero-day") || strings.Contains(lower, "暴跌") {
		score = 9
	}
	return model.ScoreResult{
		Score:           score,
		Category:        normalizeCategory(item.CategoryHint),
		ZhTitle:         truncateRunes(item.Title, 24),
		Impact:          truncateRunes(item.Summary, 60),
		Entities:        []string{},
		PrimaryTopicKey: normalizeCategory(item.CategoryHint) + ":" + item.SourceName,
	}
}

func normalizeCategory(hint string) string {
	h := strings.ToLower(hint)
	switch {
	case strings.Contains(h, "finance") || strings.Contains(h, "财经") || strings.Contains(h, "金融"):
		return "finance"
	case strings.Contains(h, "ai") || strings.Contains(h, "模型"):
		return "ai"
	case strings.Contains(h, "world") || strings.Contains(h, "国际") || strings.Contains(h, "灾害"):
		return "world"
	default:
		return "bigtech"
	}
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func sanitizeFollowUps(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, 3)
	for _, raw := range items {
		s := strings.TrimSpace(raw)
		if s == "" || seen[s] {
			continue
		}
		if len([]rune(s)) > 36 {
			s = truncateRunes(s, 34)
		}
		seen[s] = true
		out = append(out, s)
		if len(out) >= 3 {
			break
		}
	}
	return out
}
