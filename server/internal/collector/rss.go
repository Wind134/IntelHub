package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	"intel-hub/internal/config"
	"intel-hub/internal/model"
	"intel-hub/internal/store"
)

const defaultUserAgent = "Hermes-Watcher/1.0 (+intel-hub)"

// Handler processes a batch of normalized RawItems into the 5-layer pipeline.
type Handler func(ctx context.Context, items []model.RawItem)

// FeedPoller handles RSS/Atom/Status/SEC/WSCN/CISA KEV polling with ETag & baseline protection.
type FeedPoller struct {
	feeds      []config.FeedConfig
	store      *store.Store
	handler    Handler
	httpClient *http.Client
	parser     *gofeed.Parser
}

func NewFeedPoller(feeds []config.FeedConfig, st *store.Store, handler Handler) *FeedPoller {
	return &FeedPoller{
		feeds:      feeds,
		store:      st,
		handler:    handler,
		httpClient: &http.Client{Timeout: 18 * time.Second},
		parser:     gofeed.NewParser(),
	}
}

// Start launches an independent goroutine per enabled feed with staggered startup.
func (p *FeedPoller) Start(ctx context.Context) {
	for i, f := range p.feeds {
		if !f.Enabled || strings.TrimSpace(f.URL) == "" {
			continue
		}
		feed := f
		delay := time.Duration(i*700) * time.Millisecond
		go p.runOne(ctx, feed, delay)
	}
}

func (p *FeedPoller) runOne(ctx context.Context, feed config.FeedConfig, initialDelay time.Duration) {
	interval := time.Duration(feed.IntervalSec) * time.Second
	if interval < 30*time.Second {
		interval = 3 * time.Minute
	}

	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	p.pollOnce(ctx, feed)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pollOnce(ctx, feed)
		}
	}
}

func (p *FeedPoller) pollOnce(ctx context.Context, feed config.FeedConfig) {
	st, _ := p.store.GetSourceState(ctx, feed.Name)
	isFirstRun := st == nil || st.LastSuccessAt == 0

	var (
		items   []model.RawItem
		etag    string
		lastMod string
		err     error
	)

	switch feed.Type {
	case "wscn_lives":
		items, err = p.fetchWSCNLives(ctx, feed)
	case "cisa_kev":
		items, err = p.fetchCISAKEV(ctx, feed)
	default:
		prevETag, prevLastMod := "", ""
		if st != nil {
			prevETag, prevLastMod = st.ETag, st.LastModified
		}
		items, etag, lastMod, err = p.fetchRSS(ctx, feed, prevETag, prevLastMod)
	}

	if err != nil {
		fails, _ := p.store.UpsertSourceError(ctx, feed.Name, feed.Type, err.Error())
		if fails == 1 || fails%5 == 0 {
			log.Printf("[collector:%s] error (streak=%d): %v", feed.Name, fails, err)
		}
		return
	}

	if isFirstRun {
		for i := range items {
			items[i].IsBaseline = true
		}
	}
	if len(items) > 0 {
		p.handler(ctx, items)
	}
	_ = p.store.UpsertSourceSuccess(ctx, feed.Name, feed.Type, "", etag, lastMod)
}

func (p *FeedPoller) fetchRSS(ctx context.Context, feed config.FeedConfig, prevETag, prevLastMod string) ([]model.RawItem, string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feed.URL, nil)
	if err != nil {
		return nil, "", "", err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml, */*")
	if prevETag != "" {
		req.Header.Set("If-None-Match", prevETag)
	}
	if prevLastMod != "" {
		req.Header.Set("If-Modified-Since", prevLastMod)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return nil, prevETag, prevLastMod, nil
	}
	if resp.StatusCode >= 300 {
		return nil, "", "", fmt.Errorf("http %d", resp.StatusCode)
	}

	parsed, err := p.parser.Parse(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, "", "", err
	}

	nowTS := time.Now().Unix()
	out := make([]model.RawItem, 0, len(parsed.Items))
	for _, it := range parsed.Items {
		summary := it.Description
		if summary == "" {
			summary = it.Content
		}
		if feed.Type == "status_atom" && isRoutineStatusMaintenance(it.Title, summary) {
			continue
		}
		link := strings.TrimSpace(it.Link)
		if link == "" {
			link = strings.TrimSpace(it.GUID)
		}
		// Preserve publisher short alias for The Register (guid is shorter than full link).
		if strings.Contains(strings.ToLower(it.GUID), "theregister.com/a/") {
			link = strings.TrimSpace(it.GUID)
		}
		extID := strings.TrimSpace(it.GUID)
		if extID == "" {
			extID = link
		}
		pubTS := nowTS
		if it.PublishedParsed != nil {
			pubTS = clampPublishedTS(it.PublishedParsed.Unix(), nowTS)
		} else if it.UpdatedParsed != nil {
			pubTS = clampPublishedTS(it.UpdatedParsed.Unix(), nowTS)
		}
		out = append(out, model.RawItem{
			SourceName:       feed.Name,
			SourceType:       feed.Type,
			ExternalID:       extID,
			Title:            it.Title,
			Summary:          summary,
			URL:              link,
			PublishedAt:      pubTS,
			CategoryHint:     feed.Category,
			BypassRuleFilter: feed.BypassRules,
			TraditionalZh:    feed.TraditionalZh,
		})
	}
	return out, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"), nil
}

// clampPublishedTS prevents future-dated scheduled notices or timezone-broken feeds
// (e.g. Beijing local time emitted as UTC) from hijacking the top of the reverse-chronological stream.
func clampPublishedTS(ts, now int64) int64 {
	if ts <= 0 || ts > now+300 {
		return now
	}
	return ts
}

// isRoutineStatusMaintenance filters out routine scheduled PoP/datacenter maintenance notices
// from statuspage Atom feeds while keeping real outages, incidents, and degradations.
func isRoutineStatusMaintenance(title, summary string) bool {
	combined := strings.ToLower(title + " " + summary)
	isMaint := strings.Contains(combined, "scheduled maintenance") ||
		strings.Contains(combined, "planned maintenance") ||
		strings.Contains(combined, "maintenance window") ||
		strings.Contains(combined, "undergo maintenance")
	if !isMaint {
		return false
	}
	for _, severe := range []string{"outage", "disruption", "degraded", "major incident", "critical", "elevated error"} {
		if strings.Contains(combined, severe) {
			return false
		}
	}
	return true
}

// fetchWSCNLives polls WallstreetCN 7x24 public breaking news stream (free, ~60s latency).
func (p *FeedPoller) fetchWSCNLives(ctx context.Context, feed config.FeedConfig) ([]model.RawItem, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feed.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("wscn http %d", resp.StatusCode)
	}
	var payload struct {
		Data struct {
			Items []struct {
				ID          int64  `json:"id"`
				Title       string `json:"title"`
				ContentText string `json:"content_text"`
				URI         string `json:"uri"`
				DisplayTime int64  `json:"display_time"`
				Score       int    `json:"score"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&payload); err != nil {
		return nil, err
	}
	out := make([]model.RawItem, 0, len(payload.Data.Items))
	for _, it := range payload.Data.Items {
		title := strings.TrimSpace(it.Title)
		if title == "" {
			r := []rune(strings.TrimSpace(it.ContentText))
			if len(r) > 48 {
				title = string(r[:48]) + "…"
			} else {
				title = string(r)
			}
		}
		link := it.URI
		if link == "" {
			link = fmt.Sprintf("https://wallstreetcn.com/livenews/%d", it.ID)
		}
		out = append(out, model.RawItem{
			SourceName:       feed.Name,
			SourceType:       "wscn_lives",
			ExternalID:       fmt.Sprintf("wscn:%d", it.ID),
			Title:            title,
			Summary:          it.ContentText,
			URL:              link,
			PublishedAt:      clampPublishedTS(it.DisplayTime, time.Now().Unix()),
			CategoryHint:     "finance",
			BypassRuleFilter: it.Score >= 2 || feed.BypassRules,
		})
	}
	return out, nil
}

// fetchCISAKEV polls the official CISA Known Exploited Vulnerabilities JSON catalog.
func (p *FeedPoller) fetchCISAKEV(ctx context.Context, feed config.FeedConfig) ([]model.RawItem, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feed.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("cisa http %d", resp.StatusCode)
	}
	var payload struct {
		Vulnerabilities []struct {
			CVEID            string `json:"cveID"`
			VendorProject    string `json:"vendorProject"`
			Product          string `json:"product"`
			Vulnerability    string `json:"vulnerabilityName"`
			DateAdded        string `json:"dateAdded"`
			ShortDescription string `json:"shortDescription"`
		} `json:"vulnerabilities"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8*1024*1024)).Decode(&payload); err != nil {
		return nil, err
	}
	// Only inspect the latest 25 entries to stay fast.
	start := 0
	if len(payload.Vulnerabilities) > 25 {
		start = len(payload.Vulnerabilities) - 25
	}
	var out []model.RawItem
	for _, v := range payload.Vulnerabilities[start:] {
		nowCisa := time.Now().Unix()
		pubTS := nowCisa
		if t, err := time.Parse("2006-01-02", v.DateAdded); err == nil {
			pubTS = clampPublishedTS(t.Unix(), nowCisa)
		}
		out = append(out, model.RawItem{
			SourceName:       feed.Name,
			SourceType:       "cisa_kev",
			ExternalID:       v.CVEID,
			Title:            fmt.Sprintf("[CISA KEV] %s %s: %s (%s)", v.VendorProject, v.Product, v.Vulnerability, v.CVEID),
			Summary:          v.ShortDescription,
			URL:              "https://nvd.nist.gov/vuln/detail/" + v.CVEID,
			PublishedAt:      pubTS,
			CategoryHint:     "world",
			BypassRuleFilter: true,
		})
	}
	return out, nil
}
