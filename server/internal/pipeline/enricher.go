package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"intel-hub/internal/config"
	"intel-hub/internal/store"
)

// SearchResult is a compact external news hit used for stock anomaly attribution and 9+ fact-checking.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Domain  string `json:"domain"`
}

// Enricher queries Brave Search News or Tavily on demand (strictly event-driven, $0 within free tier).
type Enricher struct {
	cfg        config.SearchConfig
	store      *store.Store
	httpClient *http.Client
}

func NewEnricher(cfg config.SearchConfig, st *store.Store) *Enricher {
	return &Enricher{
		cfg:        cfg,
		store:      st,
		httpClient: &http.Client{Timeout: 12 * time.Second},
	}
}

func (e *Enricher) Enabled() bool {
	return strings.TrimSpace(e.cfg.BraveAPIKey) != "" || strings.TrimSpace(e.cfg.TavilyAPIKey) != ""
}

// SearchNews fetches up to 4 recent web/news hits for attribution or multi-source verification.
func (e *Enricher) SearchNews(ctx context.Context, query string) ([]SearchResult, error) {
	if strings.TrimSpace(e.cfg.BraveAPIKey) != "" {
		res, err := e.searchBrave(ctx, query)
		if err == nil && len(res) > 0 {
			_ = e.store.RecordAPIBudget(ctx, "brave", 1, 1, 0.0)
			return res, nil
		}
	}
	if strings.TrimSpace(e.cfg.TavilyAPIKey) != "" {
		res, err := e.searchTavily(ctx, query)
		if err == nil {
			_ = e.store.RecordAPIBudget(ctx, "tavily", 1, 1, 0.0)
			return res, nil
		}
		return nil, err
	}
	return nil, nil
}

// VerifyBreaking checks whether authoritative outlets confirm a 9-10 score breaking story.
func (e *Enricher) VerifyBreaking(ctx context.Context, query string) (status string, note string) {
	if !e.Enabled() {
		return "unverified", "未配置 Brave/Tavily 核验密钥，跳过自动多源交叉核验"
	}
	hits, err := e.SearchNews(ctx, query)
	if err != nil || len(hits) == 0 {
		return "unverified", "暂未检索到第二权威信源跟进，请注意核实原始出处"
	}

	authoritative := []string{
		"reuters.com", "bloomberg.com", "wsj.com", "cnbc.com", "ft.com", "apnews.com",
		"sec.gov", "openai.com", "anthropic.com", "blog.google", "github.com", "cisa.gov",
	}
	var confirmedDomains []string
	var headlines []string
	for _, h := range hits {
		d := strings.ToLower(h.Domain)
		for _, auth := range authoritative {
			if strings.Contains(d, auth) {
				confirmedDomains = append(confirmedDomains, auth)
				break
			}
		}
		if len(headlines) < 2 {
			headlines = append(headlines, fmt.Sprintf("[%s] %s", h.Domain, h.Title))
		}
	}
	if len(confirmedDomains) > 0 {
		return "confirmed", fmt.Sprintf("已获权威源交叉印证 (%s)：%s", strings.Join(confirmedDomains, ", "), strings.Join(headlines, " | "))
	}
	return "unverified", fmt.Sprintf("检索到相关讨论但尚无主流通讯社确认：%s", strings.Join(headlines, " | "))
}

func (e *Enricher) searchBrave(ctx context.Context, query string) ([]SearchResult, error) {
	endpoint := "https://api.search.brave.com/res/v1/news/search?q=" + url.QueryEscape(query) + "&count=4&freshness=pd"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", e.cfg.BraveAPIKey)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("brave status %d", resp.StatusCode)
	}
	var payload struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 128*1024)).Decode(&payload); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(payload.Results))
	for _, r := range payload.Results {
		u, _ := url.Parse(r.URL)
		out = append(out, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Description,
			Domain:  u.Hostname(),
		})
	}
	return out, nil
}

func (e *Enricher) searchTavily(ctx context.Context, query string) ([]SearchResult, error) {
	body, _ := json.Marshal(map[string]any{
		"api_key":      e.cfg.TavilyAPIKey,
		"query":        query,
		"topic":        "news",
		"search_depth": "basic",
		"max_results":  4,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tavily status %d", resp.StatusCode)
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 128*1024)).Decode(&payload); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(payload.Results))
	for _, r := range payload.Results {
		u, _ := url.Parse(r.URL)
		out = append(out, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
			Domain:  u.Hostname(),
		})
	}
	return out, nil
}
