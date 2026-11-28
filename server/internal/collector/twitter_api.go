package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"intel-hub/internal/config"
	"intel-hub/internal/model"
	"intel-hub/internal/store"
)

const (
	twitterCreditsPerTweet = 15
	twitterUSDPerCredit    = 0.00001 // $1.00 = 100,000 credits => 15 credits = $0.00015
)

// TwitterAPICollector polls TwitterAPI.io with combined `from:a OR from:b` queries,
// dynamic active/quiet intervals, and strict SQLite budget circuit-breaking.
type TwitterAPICollector struct {
	cfg        config.TwitterAPIConfig
	budgetCfg  config.BudgetConfig
	store      *store.Store
	handler    Handler
	httpClient *http.Client
}

func NewTwitterAPICollector(cfg config.TwitterAPIConfig, budgetCfg config.BudgetConfig, st *store.Store, handler Handler) *TwitterAPICollector {
	return &TwitterAPICollector{
		cfg:        cfg,
		budgetCfg:  budgetCfg,
		store:      st,
		handler:    handler,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (t *TwitterAPICollector) Start(ctx context.Context) {
	if !t.cfg.Enabled || strings.TrimSpace(t.cfg.APIKey) == "" {
		return
	}
	if t.cfg.QueryOverride == "" && len(t.cfg.Accounts) == 0 {
		return
	}
	go t.loop(ctx)
}

func (t *TwitterAPICollector) loop(ctx context.Context) {
	log.Printf("[collector:twitterapi_io] started (%d accounts, cap=$%.2f/mo)", len(t.cfg.Accounts), t.budgetCfg.TwitterCapUSD)
	t.pollOnce(ctx)
	for {
		wait := t.nextInterval()
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
			t.pollOnce(ctx)
		}
	}
}

// nextInterval uses faster polling during US/Asia active market hours (UTC+8 08:00-04:00 next day = 14h active).
func (t *TwitterAPICollector) nextInterval() time.Duration {
	shanghai := time.FixedZone("Asia/Shanghai", 8*3600)
	hr := time.Now().In(shanghai).Hour()
	active := (hr >= 9 && hr <= 15) || hr >= 20 || hr <= 4
	sec := t.cfg.QuietIntervalSec
	if active {
		sec = t.cfg.ActiveIntervalSec
	}
	if sec < 60 {
		sec = 120
	}
	return time.Duration(sec) * time.Second
}

func (t *TwitterAPICollector) pollOnce(ctx context.Context) {
	// Hard budget circuit breaker before spending a single credit.
	budget, err := t.store.GetMonthlyBudget(ctx, t.budgetCfg.MonthlyCapUSD)
	if err == nil {
		if budget.Tripped {
			log.Printf("[collector:twitterapi_io] monthly budget cap reached ($%.2f >= $%.2f), skipping", budget.TotalUSD, t.budgetCfg.MonthlyCapUSD)
			return
		}
		for _, p := range budget.Providers {
			if p.Provider == "twitterapi_io" && p.EstimatedUSD >= t.budgetCfg.TwitterCapUSD {
				log.Printf("[collector:twitterapi_io] twitter cap reached ($%.2f >= $%.2f), skipping", p.EstimatedUSD, t.budgetCfg.TwitterCapUSD)
				return
			}
		}
	}

	st, _ := t.store.GetSourceState(ctx, "twitterapi_io")
	isFirstRun := st == nil || st.LastSuccessAt == 0
	sinceID := ""
	if st != nil {
		sinceID = st.CursorValue
	}

	query := t.buildQuery(sinceID)
	endpoint := "https://api.twitterapi.io/twitter/tweet/advanced_search?queryType=Latest&query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return
	}
	req.Header.Set("X-API-Key", t.cfg.APIKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		_, _ = t.store.UpsertSourceError(ctx, "twitterapi_io", "twitter_api", err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		_, _ = t.store.UpsertSourceError(ctx, "twitterapi_io", "twitter_api", fmt.Sprintf("http %d: %s", resp.StatusCode, string(body)))
		return
	}

	var payload struct {
		Tweets []twitterTweet `json:"tweets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&payload); err != nil {
		_, _ = t.store.UpsertSourceError(ctx, "twitterapi_io", "twitter_api", err.Error())
		return
	}

	// Accurately bill per TwitterAPI.io rules: minimum 15 credits per request, 15 credits per returned tweet.
	returned := int64(len(payload.Tweets))
	credits := returned * twitterCreditsPerTweet
	if credits < twitterCreditsPerTweet {
		credits = twitterCreditsPerTweet
	}
	_ = t.store.RecordAPIBudget(ctx, "twitterapi_io", 1, credits, float64(credits)*twitterUSDPerCredit)

	maxID := sinceID
	items := make([]model.RawItem, 0, len(payload.Tweets))
	for _, tw := range payload.Tweets {
		if tw.ID > maxID || len(tw.ID) > len(maxID) {
			maxID = tw.ID
		}
		item, keep := buildTweetRawItem(tw, isFirstRun)
		if !keep {
			continue
		}
		items = append(items, item)
	}

	if len(items) > 0 {
		t.handler(ctx, items)
	}
	_ = t.store.UpsertSourceSuccess(ctx, "twitterapi_io", "twitter_api", maxID, "", "")
}

func (t *TwitterAPICollector) buildQuery(sinceID string) string {
	base := strings.TrimSpace(t.cfg.QueryOverride)
	if base == "" {
		parts := make([]string, 0, len(t.cfg.Accounts))
		for _, a := range t.cfg.Accounts {
			a = strings.TrimPrefix(strings.TrimSpace(a), "@")
			if a != "" {
				parts = append(parts, "from:"+a)
			}
		}
		base = "(" + strings.Join(parts, " OR ") + ") -is:retweet -is:reply"
	}
	if sinceID != "" {
		base += " since_id:" + sinceID
	}
	return base
}

type twitterAuthor struct {
	UserName string `json:"userName"`
	Name     string `json:"name"`
}

type twitterQuoted struct {
	ID     string        `json:"id"`
	Text   string        `json:"text"`
	Author twitterAuthor `json:"author"`
}

type twitterTweet struct {
	ID                string         `json:"id"`
	URL               string         `json:"url"`
	Text              string         `json:"text"`
	CreatedAt         string         `json:"createdAt"`
	IsReply           bool           `json:"isReply"`
	InReplyToID       string         `json:"inReplyToId"`
	InReplyToUsername string         `json:"inReplyToUsername"`
	Author            twitterAuthor  `json:"author"`
	QuotedTweet       *twitterQuoted `json:"quoted_tweet"`
}

var lowSignalReplies = map[string]bool{
	"yes": true, "yup": true, "yeah": true, "yep": true, "no": true, "nope": true,
	"true": true, "indeed": true, "exactly": true, "wow": true, "cool": true,
	"interesting": true, "agreed": true, "correct": true, "thanks": true, "thank you": true,
	"100%": true, "haha": true, "lol": true, "lmao": true, "nice": true, "great": true,
	"good point": true, "makes sense": true, "fair point": true, "looking into it": true,
	"working on it": true, "coming soon": true, "for sure": true, "definitely": true,
}

func stripTweetNoise(s string) string {
	fields := strings.Fields(s)
	var kept []string
	for _, f := range fields {
		lf := strings.ToLower(f)
		if strings.HasPrefix(lf, "http://") || strings.HasPrefix(lf, "https://") || strings.HasPrefix(f, "@") {
			continue
		}
		kept = append(kept, f)
	}
	return strings.TrimSpace(strings.Join(kept, " "))
}

func countMeaningfulRunes(s string) int {
	n := 0
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r >= 0x4E00 {
			n++
		}
	}
	return n
}

// buildTweetRawItem 过滤无上下文的碎片回复/表情水推，并为引用推文强制补齐原推上下文背景。
func buildTweetRawItem(tw twitterTweet, isFirstRun bool) (model.RawItem, bool) {
	cleanSelf := stripTweetNoise(tw.Text)
	selfKey := strings.ToLower(strings.Trim(cleanSelf, " .!?,:;\"'"))
	selfLen := countMeaningfulRunes(cleanSelf)

	quotedText := ""
	quotedUser := ""
	if tw.QuotedTweet != nil {
		quotedText = stripTweetNoise(tw.QuotedTweet.Text)
		quotedUser = strings.TrimSpace(tw.QuotedTweet.Author.UserName)
	}
	quotedLen := countMeaningfulRunes(quotedText)

	// 1. 严格丢弃无上下文的纯回复碎片（如仅回复 Yes/Yup/表情 或未携带原推背景的短回复）
	if tw.IsReply && quotedLen < 25 {
		if selfLen < 60 || lowSignalReplies[selfKey] {
			return model.RawItem{}, false
		}
	}

	// 2. 丢弃无引用原推时的纯表情、单词表态或缺乏背景的极短水推（如 "❤️", "Yes", "It is very useful"）
	if quotedLen == 0 {
		if selfLen < 32 || lowSignalReplies[selfKey] {
			return model.RawItem{}, false
		}
	} else {
		// 有引用原推时，若原推本身也极短且作者仅发表情/敷衍词，同样丢弃
		if quotedLen < 35 && (selfLen < 12 || lowSignalReplies[selfKey]) {
			return model.RawItem{}, false
		}
	}

	pubTS := time.Now().Unix()
	if parsed, err := time.Parse(time.RubyDate, tw.CreatedAt); err == nil {
		pubTS = parsed.Unix()
	} else if parsed, err := time.Parse(time.RFC3339, tw.CreatedAt); err == nil {
		pubTS = parsed.Unix()
	}
	link := tw.URL
	if link == "" && tw.Author.UserName != "" {
		link = fmt.Sprintf("https://x.com/%s/status/%s", tw.Author.UserName, tw.ID)
	}

	title := fmt.Sprintf("@%s: %s", tw.Author.UserName, firstLine(cleanSelf, 100))
	summary := strings.TrimSpace(tw.Text)
	if quotedLen > 0 {
		qLabel := "原推"
		if quotedUser != "" {
			qLabel = "@" + quotedUser
		}
		if selfLen < 20 || lowSignalReplies[selfKey] {
			title = fmt.Sprintf("@%s 转发评论 %s: %s", tw.Author.UserName, qLabel, firstLine(quotedText, 84))
		} else {
			title = fmt.Sprintf("@%s 评 %s: %s", tw.Author.UserName, qLabel, firstLine(cleanSelf, 84))
		}
		summary = fmt.Sprintf("【原推上下文 · %s】%s\n\n【@%s 回复/评论】%s", qLabel, quotedText, tw.Author.UserName, strings.TrimSpace(tw.Text))
	} else if tw.IsReply && tw.InReplyToUsername != "" {
		summary = fmt.Sprintf("【回复 @%s】%s", tw.InReplyToUsername, strings.TrimSpace(tw.Text))
	}

	return model.RawItem{
		SourceName:       "x:@" + tw.Author.UserName,
		SourceType:       "twitter_api",
		ExternalID:       "x:" + tw.ID,
		Title:            title,
		Summary:          summary,
		URL:              link,
		PublishedAt:      pubTS,
		CategoryHint:     inferXCategory(tw.Author.UserName, title+" "+summary),
		BypassRuleFilter: false,
		IsBaseline:       isFirstRun,
	}, true
}

func firstLine(s string, maxRunes int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(strings.TrimSpace(s))
	if len(r) <= maxRunes {
		return string(r)
	}
	return string(r[:maxRunes]) + "…"
}

func inferXCategory(handle, text string) string {
	h := strings.ToLower(handle + " " + text)
	switch {
	case strings.Contains(h, "openai") || strings.Contains(h, "anthropic") || strings.Contains(h, "deepseek") || strings.Contains(h, "qwen") || strings.Contains(h, "sama") || strings.Contains(h, "karpathy"):
		return "ai"
	case strings.Contains(h, "bloomberg") || strings.Contains(h, "deltaone") || strings.Contains(h, "fed") || strings.Contains(h, "earnings") || strings.Contains(h, "cpi"):
		return "finance"
	default:
		return "bigtech"
	}
}
