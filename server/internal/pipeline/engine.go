package pipeline

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"intel-hub/internal/config"
	"intel-hub/internal/model"
	"intel-hub/internal/scorer"
	"intel-hub/internal/store"
)

// EventSink receives newly created or verified events for realtime WebSocket / FCM / Feishu delivery.
type EventSink interface {
	OnEventCreated(ctx context.Context, ev *model.IntelEvent)
	OnEventVerified(ctx context.Context, ev *model.IntelEvent)
}

// Engine executes the 5-layer intelligence funnel (Normalize -> Dedup -> Rules -> Score/Enrich -> Route/Verify).
type Engine struct {
	cfg      *config.Config
	store    *store.Store
	rules    *RuleFilter
	scorer   *scorer.Client
	enricher *Enricher
	sink     EventSink
}

func NewEngine(cfg *config.Config, st *store.Store, sc *scorer.Client, en *Enricher, sink EventSink) *Engine {
	return &Engine{
		cfg:      cfg,
		store:    st,
		rules:    NewRuleFilter(cfg.Filters),
		scorer:   sc,
		enricher: en,
		sink:     sink,
	}
}

// ProcessBatch feeds a slice of raw collector items through the 5-layer funnel.
func (e *Engine) ProcessBatch(ctx context.Context, items []model.RawItem) {
	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		e.ProcessOne(ctx, item)
	}
}

// ProcessOne runs a single RawItem through L1-L5. Returns the stored event if persisted.
func (e *Engine) ProcessOne(ctx context.Context, raw model.RawItem) *model.IntelEvent {
	// L1: Normalize text, canonicalize URL, convert Traditional Chinese if flagged.
	raw.Title = CleanText(raw.Title, raw.TraditionalZh)
	raw.Summary = CleanSummaryText(raw.Summary, raw.TraditionalZh)
	raw.URL = CanonicalizeURL(raw.URL)
	if raw.Title == "" {
		return nil
	}

	extID := raw.ExternalID
	if extID == "" {
		extID = raw.URL
	}
	sourceUID := SHA256Hex(raw.SourceName + ":" + extID)
	urlHash := ""
	if raw.URL != "" {
		urlHash = SHA256Hex(raw.URL)
	}
	simhash := ComputeSimHash64(raw.Title + " " + truncateStr(raw.Summary, 240))

	// First-run baseline protection: silently record fingerprints without scoring or alerting.
	if raw.IsBaseline {
		_ = e.store.RecordFingerprintOnly(ctx, sourceUID, urlHash, simhash, raw.Title)
		return nil
	}

	// L2: Exact + 64-bit SimHash dedup
	isDup, _, err := e.store.CheckAndRecordFingerprint(ctx, sourceUID, urlHash, simhash, raw.Title, e.cfg.Filters.SimHashThreshold)
	if err != nil || isDup {
		return nil
	}

	// L3: Zero-cost hard rule filter
	pass, _ := e.rules.Match(raw)
	if !pass {
		return nil
	}

	// L4a: On-demand attribution search if triggered by Finnhub stock price anomaly
	extraContext := ""
	if raw.SourceType == "finnhub_ws" && e.enricher != nil && e.enricher.Enabled() {
		q := extractFirstToken(raw.Title) + " stock breaking news"
		if hits, err := e.enricher.SearchNews(ctx, q); err == nil && len(hits) > 0 {
			var lines []string
			for _, h := range hits {
				lines = append(lines, fmt.Sprintf("- [%s] %s: %s", h.Domain, h.Title, h.Snippet))
			}
			extraContext = strings.Join(lines, "\n")
		}
	}

	// L4b: Fast AI Judge scoring
	scored, tokens, err := e.scorer.ScoreEvent(ctx, raw, extraContext)
	if err != nil {
		log.Printf("[pipeline] scorer warning (%s): %v", raw.SourceName, err)
	}
	if tokens > 0 {
		// Conservative Flash token estimate: ~$0.20 / 1M tokens
		_ = e.store.RecordAPIBudget(ctx, "llm_scorer", 1, tokens, float64(tokens)*0.0000002)
	}

	// L5: Severity tiering & cooldown routing
	tier := model.TierFromScore(scored.Score)
	pushStatus := "archived"
	if scored.Score >= 8 {
		pushStatus = "pushed"
	} else if scored.Score >= 5 {
		pushStatus = "digested"
	}

	verificationStatus := "none"
	if scored.Score >= 9 && raw.SourceType != "status_atom" && raw.SourceType != "cisa_kev" && raw.SourceType != "sec_atom" {
		verificationStatus = "pending"
	}

	// Defensive guard: never allow future-dated published_at (> now + 5m) to hijack the live feed.
	nowTS := time.Now().Unix()
	if raw.PublishedAt <= 0 || raw.PublishedAt > nowTS+300 {
		raw.PublishedAt = nowTS
	}

	ev, err := e.store.InsertEvent(ctx, &model.IntelEvent{
		SourceName:         raw.SourceName,
		Category:           scored.Category,
		RawTitle:           raw.Title,
		ZhTitle:            scored.ZhTitle,
		Summary:            truncateStr(raw.Summary, 1600),
		ImpactAnalysis:     scored.Impact,
		CanonicalURL:       raw.URL,
		Entities:           scored.Entities,
		QuoteSnapshot:      raw.QuoteSnapshot,
		Score:              scored.Score,
		Tier:               tier,
		PushStatus:         pushStatus,
		VerificationStatus: verificationStatus,
		PublishedAt:        raw.PublishedAt,
	})
	if err != nil {
		log.Printf("[pipeline] insert event failed: %v", err)
		return nil
	}

	// Apply 30-minute per-entity cooldown for P1 push notifications
	if ev.Score >= 8 && scored.PrimaryTopicKey != "" {
		cooled, _ := e.store.CheckAndSetCooldown(ctx, scored.PrimaryTopicKey, ev.ID, ev.Score, time.Duration(e.cfg.Filters.CooldownMinutes)*time.Minute)
		if cooled {
			ev.PushStatus = "cooled_down"
		}
	}

	if e.sink != nil {
		e.sink.OnEventCreated(ctx, ev)
	}

	// Async multi-source cross-verification for 9-10 score breaking news
	if verificationStatus == "pending" && e.enricher != nil && e.enricher.Enabled() {
		evCopy := *ev
		go e.asyncFactCheck(evCopy)
	}

	return ev
}

func (e *Engine) asyncFactCheck(ev model.IntelEvent) {
	// Brief wait so second authoritative wire can publish
	time.Sleep(35 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	query := ev.RawTitle
	if len(ev.Entities) > 0 {
		query = strings.Join(ev.Entities, " ") + " " + ev.RawTitle
	}
	status, note := e.enricher.VerifyBreaking(ctx, truncateStr(query, 120))
	updated, err := e.store.UpdateEventVerification(ctx, ev.ID, status, note)
	if err != nil || updated == nil {
		return
	}
	if e.sink != nil {
		e.sink.OnEventVerified(ctx, updated)
	}
}

func extractFirstToken(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return s
	}
	return fields[0]
}

func truncateStr(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}
