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
	mu         sync.RWMutex
	cfg        config.LLMConfig
	httpClient *http.Client
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

type chatReq struct {
	Model       string    `json:"model"`
	Messages    []chatMsg `json:"messages"`
	Temperature float64   `json:"temperature"`
	Stream      bool      `json:"stream,omitempty"`
}

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResp struct {
	Choices []struct {
		Message chatMsg `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		TotalTokens      int64 `json:"total_tokens"`
	} `json:"usage"`
}

// ScoreEvent runs L4 fast AI judge scoring. Falls back to heuristic scoring if LLM API key is absent.
func (c *Client) ScoreEvent(ctx context.Context, item model.RawItem, extraContext string) (model.ScoreResult, int64, error) {
	c.mu.RLock()
	apiKey := c.cfg.APIKey
	baseURL := c.cfg.BaseURL
	fastModel := c.cfg.FastModel
	fallbackModel := c.cfg.FallbackModel
	c.mu.RUnlock()

	if strings.TrimSpace(apiKey) == "" {
		return heuristicScore(item), 0, nil
	}

	sysPrompt := `你是全天候金融与科技突发情报裁判。请评估以下消息的突发紧迫度与行业影响，严格仅输出一个 JSON 对象，不要包含 Markdown 代码块或任何多余文字：
{"score":1-10整数,"category":"finance|ai|bigtech|world","zh_title":"18字内高密度简体中文标题","impact":"60字内核心事实与市场/行业影响推演","entities":["大写英文代码或中文实体名"],"primary_topic_key":"category:核心实体大写"}
评分标准：
9-10分(P1特急)：重大黑天鹅、美股核心标的剧烈异动(>4%)、美联储意外决议、旗舰基座模型发布/开源、主力AI API大面积宕机、百亿美元并购/反垄断重罚、影响全球供应链的重大冲突或0-day。
8分(P1重要)：明确高价值突发新闻、重要高管变动、大厂战略转向、核心芯片/AI重要进展。
5-7分(P2次级)：有价值的行业动态、财报常规解读、普通产品更新，适合归入晚间日报。
1-4分(噪音)：观点水文、软广、无实质增量的常规报道。`

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
		content, tokens, err := c.completeOnce(ctx, baseURL, apiKey, mdl, "intel-hub-scorer", []chatMsg{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: userText},
		}, 0.1)
		if err != nil {
			lastErr = err
			continue
		}
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
		return res, tokens, nil
	}

	return heuristicScore(item), 0, fmt.Errorf("llm scoring fallback used: %w", lastErr)
}

// StreamAsk streams a deep analysis answer (SSE token deltas) with automatic transient retry
// and graceful model fallback (strong -> fallback -> fast) if upstream fails before first token.
func (c *Client) StreamAsk(ctx context.Context, systemPrompt, userPrompt string, history []model.EventThread, onDelta func(delta string) error) (fullText string, modelUsed string, err error) {
	c.mu.RLock()
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
			text, streamErr := c.streamOnce(ctx, streamClient, baseURL, apiKey, mdl, msgs, func(d string) error {
				emitted = true
				return onDelta(d)
			})
			if streamErr == nil && strings.TrimSpace(text) != "" {
				return text, mdl, nil
			}
			if emitted {
				// Partial output already reached client; do not restart or duplicate answer.
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

func (c *Client) streamOnce(ctx context.Context, streamClient *http.Client, baseURL, apiKey, modelName string, msgs []chatMsg, onDelta func(delta string) error) (string, error) {
	body, _ := json.Marshal(chatReq{
		Model:       modelName,
		Messages:    msgs,
		Temperature: 0.3,
		Stream:      true,
	})

	endpoint := strings.TrimRight(baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if strings.Contains(strings.ToLower(baseURL), "opencode.ai") {
		req.Header.Set("x-opencode-session", fmt.Sprintf("infohub-ask-%d", time.Now().UnixNano()))
	}

	resp, err := streamClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("llm stream status %d (%s): %s", resp.StatusCode, modelName, strings.TrimSpace(string(errBody)))
	}

	var full strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			if payload == "[DONE]" {
				break
			}
			continue
		}
		var chunk struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err == nil {
			if chunk.Error != nil && chunk.Error.Message != "" {
				return full.String(), fmt.Errorf("upstream error (%s): %s", modelName, chunk.Error.Message)
			}
			if len(chunk.Choices) > 0 {
				delta := chunk.Choices[0].Delta.Content
				if delta != "" {
					full.WriteString(delta)
					if err := onDelta(delta); err != nil {
						return full.String(), err
					}
				}
			}
		}
	}
	return full.String(), sc.Err()
}

// GenerateDailyDigest synthesizes a structured Chinese Markdown executive briefing from pending P2 + P1 items.
func (c *Client) GenerateDailyDigest(ctx context.Context, dateStr string, p1Events []model.IntelEvent, p2Items []model.DigestItem) (string, string, error) {
	c.mu.RLock()
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
	return strings.TrimSpace(parsed.Choices[0].Message.Content), mdl, nil
}

func (c *Client) completeOnce(ctx context.Context, baseURL, apiKey, modelName, sessionID string, msgs []chatMsg, temp float64) (string, int64, error) {
	body, _ := json.Marshal(chatReq{
		Model:       modelName,
		Messages:    msgs,
		Temperature: temp,
	})
	endpoint := strings.TrimRight(baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if strings.Contains(strings.ToLower(baseURL), "opencode.ai") {
		req.Header.Set("x-opencode-session", sessionID)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("status %d: %s", resp.StatusCode, string(raw))
	}
	var parsed chatResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", 0, err
	}
	if len(parsed.Choices) == 0 {
		return "", 0, fmt.Errorf("empty choices from model %s", modelName)
	}
	return parsed.Choices[0].Message.Content, parsed.Usage.TotalTokens, nil
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
