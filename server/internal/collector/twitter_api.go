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
		Tweets []struct {
			ID        string `json:"id"`
			URL       string `json:"url"`
			Text      string `json:"text"`
			CreatedAt string `json:"createdAt"`
			Author    struct {
				UserName string `json:"userName"`
				Name     string `json:"name"`
			} `json:"author"`
		} `json:"tweets"`
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
		items = append(items, model.RawItem{
			SourceName:       "x:@" + tw.Author.UserName,
			SourceType:       "twitter_api",
			ExternalID:       "x:" + tw.ID,
			Title:            fmt.Sprintf("@%s: %s", tw.Author.UserName, firstLine(tw.Text, 100)),
			Summary:          tw.Text,
			URL:              link,
			PublishedAt:      pubTS,
			CategoryHint:     inferXCategory(tw.Author.UserName, tw.Text),
			BypassRuleFilter: false,
			IsBaseline:       isFirstRun,
		})
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
