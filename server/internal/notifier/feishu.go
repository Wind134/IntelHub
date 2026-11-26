package notifier

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"intel-hub/internal/config"
	"intel-hub/internal/model"
)

var shanghaiLoc = time.FixedZone("Asia/Shanghai", 8*3600)

// FeishuNotifier sends Interactive Cards via Webhook or Feishu App Bot API.
type FeishuNotifier struct {
	cfg        config.NotifyConfig
	httpClient *http.Client

	mu          sync.Mutex
	tenantToken string
	tokenExp    time.Time
}

func NewFeishuNotifier(cfg config.NotifyConfig) *FeishuNotifier {
	return &FeishuNotifier{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 12 * time.Second},
	}
}

func (f *FeishuNotifier) Enabled() bool {
	return strings.TrimSpace(f.cfg.FeishuWebhook) != "" ||
		(strings.TrimSpace(f.cfg.FeishuAppID) != "" && strings.TrimSpace(f.cfg.FeishuAppSecret) != "" && strings.TrimSpace(f.cfg.FeishuChatID) != "")
}

func (f *FeishuNotifier) SendEventCard(ctx context.Context, ev *model.IntelEvent) (string, error) {
	if !f.Enabled() {
		return "", nil
	}
	card := buildFeishuCard(ev)
	if strings.TrimSpace(f.cfg.FeishuWebhook) != "" {
		return "", f.sendWebhook(ctx, card)
	}
	return f.sendAppMessage(ctx, card)
}

func (f *FeishuNotifier) SendVerificationUpdate(ctx context.Context, ev *model.IntelEvent) error {
	if !f.Enabled() || ev.VerificationNote == "" {
		return nil
	}
	badge := "🔍 多源核验补充"
	if ev.VerificationStatus == "confirmed" {
		badge = "✅ 权威信源已交叉印证"
	}
	verifyCard := map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": "blue",
			"title": map[string]any{
				"tag":     "plain_text",
				"content": fmt.Sprintf("%s | %s", badge, ev.EventCode),
			},
		},
		"elements": []any{
			map[string]any{
				"tag":     "markdown",
				"content": fmt.Sprintf("**核验结论**\n%s", ev.VerificationNote),
			},
		},
	}

	if strings.TrimSpace(f.cfg.FeishuWebhook) != "" {
		return f.sendWebhook(ctx, verifyCard)
	}

	// Prefer attaching inside the original message's thread (话题回复) so main chat stays clean.
	// If the chat is a 1-on-1 DM where reply_in_thread is unsupported, seamlessly PATCH the original card in-place!
	if strings.TrimSpace(ev.FeishuMessageID) != "" {
		if err := f.replyInThread(ctx, ev.FeishuMessageID, verifyCard); err == nil {
			return nil
		}
		if err := f.patchAppCard(ctx, ev.FeishuMessageID, buildFeishuCard(ev)); err == nil {
			return nil
		}
	}
	_, err := f.sendAppMessage(ctx, verifyCard)
	return err
}

func buildFeishuCard(ev *model.IntelEvent) map[string]any {
	template := "orange"
	level := "P1 突发"
	if ev.Score >= 9 {
		template = "red"
		level = "P1 特急"
	}
	pubStr := time.Unix(ev.PublishedAt, 0).In(shanghaiLoc).Format("2006-01-02 15:04 (UTC+8)")
	catLabel := categoryLabel(ev.Category)

	var sections []string
	if strings.TrimSpace(ev.ImpactAnalysis) != "" {
		sections = append(sections, fmt.Sprintf("**核心影响**\n%s", strings.TrimSpace(ev.ImpactAnalysis)))
	}
	if strings.TrimSpace(ev.Summary) != "" {
		sections = append(sections, fmt.Sprintf("**事件摘要**\n%s", truncateRunes(strings.TrimSpace(ev.Summary), 260)))
	}
	if strings.TrimSpace(ev.VerificationNote) != "" {
		vTitle := "**🔍 多源核验补充**"
		if ev.VerificationStatus == "confirmed" {
			vTitle = "**✅ 权威信源已交叉印证**"
		}
		sections = append(sections, fmt.Sprintf("%s\n%s", vTitle, strings.TrimSpace(ev.VerificationNote)))
	}
	metaLines := []string{
		"**情报元信息**",
		fmt.Sprintf("　• 来源：%s", ev.SourceName),
		fmt.Sprintf("　• 时间：%s", pubStr),
		fmt.Sprintf("　• 编号：`#%s`", ev.EventCode),
	}
	sections = append(sections, strings.Join(metaLines, "\n"))

	elements := []any{
		map[string]any{"tag": "markdown", "content": strings.Join(sections, "\n\n")},
	}
	if ev.CanonicalURL != "" {
		elements = append(elements, map[string]any{
			"tag": "action",
			"actions": []any{
				map[string]any{
					"tag":  "button",
					"type": "primary",
					"text": map[string]any{"tag": "plain_text", "content": "查看原文"},
					"url":  ev.CanonicalURL,
				},
			},
		})
	}
	elements = append(elements, map[string]any{
		"tag": "note",
		"elements": []any{
			map[string]any{
				"tag":     "plain_text",
				"content": fmt.Sprintf("可直接回复或 @Hermes 追问 #%s，或在客户端流式深挖", ev.EventCode),
			},
		},
	})

	return map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": template,
			"title": map[string]any{
				"tag":     "plain_text",
				"content": fmt.Sprintf("[%s · %s %d/10] %s", level, catLabel, ev.Score, ev.ZhTitle),
			},
		},
		"elements": elements,
	}
}

func (f *FeishuNotifier) sendWebhook(ctx context.Context, card map[string]any) error {
	payload := map[string]any{
		"msg_type": "interactive",
		"card":     card,
	}
	if f.cfg.FeishuSecret != "" {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		payload["timestamp"] = ts
		payload["sign"] = feishuSign(ts, f.cfg.FeishuSecret)
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.cfg.FeishuWebhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("feishu webhook http %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

func (f *FeishuNotifier) sendAppMessage(ctx context.Context, card map[string]any) (string, error) {
	token, err := f.getTenantToken(ctx)
	if err != nil {
		return "", err
	}
	cardJSON, _ := json.Marshal(card)
	body, _ := json.Marshal(map[string]any{
		"receive_id": f.cfg.FeishuChatID,
		"msg_type":   "interactive",
		"content":    string(cardJSON),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=chat_id", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("feishu app im http %d: %s", resp.StatusCode, string(raw))
	}
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err == nil {
		if parsed.Code != 0 {
			return "", fmt.Errorf("feishu app im code %d: %s", parsed.Code, parsed.Msg)
		}
		return parsed.Data.MessageID, nil
	}
	return "", nil
}

func (f *FeishuNotifier) replyInThread(ctx context.Context, messageID string, card map[string]any) error {
	token, err := f.getTenantToken(ctx)
	if err != nil {
		return err
	}
	cardJSON, _ := json.Marshal(card)
	body, _ := json.Marshal(map[string]any{
		"msg_type":        "interactive",
		"content":         string(cardJSON),
		"reply_in_thread": true,
	})
	endpoint := fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/messages/%s/reply", messageID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("feishu reply http %d: %s", resp.StatusCode, string(raw))
	}
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(raw, &parsed); err == nil && parsed.Code != 0 {
		return fmt.Errorf("feishu reply code %d: %s", parsed.Code, parsed.Msg)
	}
	return nil
}

func (f *FeishuNotifier) patchAppCard(ctx context.Context, messageID string, card map[string]any) error {
	token, err := f.getTenantToken(ctx)
	if err != nil {
		return err
	}
	cardJSON, _ := json.Marshal(card)
	body, _ := json.Marshal(map[string]any{
		"content": string(cardJSON),
	})
	endpoint := fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/messages/%s", messageID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("feishu patch http %d: %s", resp.StatusCode, string(raw))
	}
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(raw, &parsed); err == nil && parsed.Code != 0 {
		return fmt.Errorf("feishu patch code %d: %s", parsed.Code, parsed.Msg)
	}
	return nil
}

func (f *FeishuNotifier) getTenantToken(ctx context.Context) (string, error) {
	f.mu.Lock()
	if f.tenantToken != "" && time.Now().Before(f.tokenExp) {
		tok := f.tenantToken
		f.mu.Unlock()
		return tok, nil
	}
	f.mu.Unlock()

	body, _ := json.Marshal(map[string]string{
		"app_id":     f.cfg.FeishuAppID,
		"app_secret": f.cfg.FeishuAppSecret,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var parsed struct {
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
		Msg               string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	if parsed.TenantAccessToken == "" {
		return "", fmt.Errorf("feishu token failed: %s", parsed.Msg)
	}
	f.mu.Lock()
	f.tenantToken = parsed.TenantAccessToken
	f.tokenExp = time.Now().Add(time.Duration(parsed.Expire-120) * time.Second)
	f.mu.Unlock()
	return parsed.TenantAccessToken, nil
}

func feishuSign(timestamp, secret string) string {
	strToSign := timestamp + "\n" + secret
	h := hmac.New(sha256.New, []byte(strToSign))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// FormatWeixinLine enforces the <=120-char clickable Markdown link rule from weixin-news-alerts.
func FormatWeixinLine(prefix, title, link string) string {
	link = strings.TrimSpace(link)
	if len(link) > 100 || link == "" {
		return ""
	}
	// Format: "<prefix> [<title>](<link>)"
	overhead := len([]rune(prefix)) + 1 + 4 + len([]rune(link))
	budget := 120 - overhead
	if budget < 4 {
		return fmt.Sprintf("%s [链接](%s)", prefix, link)
	}
	titleRunes := []rune(strings.TrimSpace(title))
	if len(titleRunes) > budget {
		title = string(titleRunes[:budget-3]) + "..."
	}
	return fmt.Sprintf("%s [%s](%s)", prefix, title, link)
}

func categoryLabel(cat string) string {
	switch cat {
	case "finance":
		return "金融"
	case "ai":
		return "AI"
	case "world":
		return "全球"
	default:
		return "大厂"
	}
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
