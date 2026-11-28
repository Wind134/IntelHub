package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"intel-hub/internal/model"
)

func TestStoreLifecycleSearchTimelineAndBudget(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test_intel.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// 1. Fingerprint exact + SimHash Hamming distance <= 3 detection
	sim1 := uint64(0x123456789ABCDEF0)
	sim2 := sim1 ^ 0x0000000000000007 // Hamming distance 3 (3 bits flipped)
	dup, _, err := st.CheckAndRecordFingerprint(ctx, "uid1", "url1", sim1, "Nvidia subpoena", 3)
	if err != nil || dup {
		t.Fatalf("first insert should not be dup: dup=%v err=%v", dup, err)
	}
	dup, reason, err := st.CheckAndRecordFingerprint(ctx, "uid2", "url2", sim2, "Nvidia gets DOJ subpoena", 3)
	if err != nil || !dup || reason != "simhash" {
		t.Fatalf("expected simhash dup, got dup=%v reason=%q err=%v", dup, reason, err)
	}

	// 2. Insert historical events & test CJK FTS5 search + Entity timeline
	now := time.Now().Unix()
	ev1, err := st.InsertEvent(ctx, &model.IntelEvent{
		SourceName:     "twitter:walter",
		Category:       "finance",
		RawTitle:       "NVDA drops 6% after DOJ antitrust subpoena",
		ZhTitle:        "英伟达盘后急跌6%：收到反垄断传票",
		Summary:        "美国司法部向英伟达发出反垄断调查传票，影响AI算力芯片供应链。",
		ImpactAnalysis: "短期压制半导体估值，关注台积电联动。",
		CanonicalURL:   "https://example.com/nvda-1",
		Entities:       []string{"NVDA", "TSM", "DOJ"},
		Score:          9,
		PushStatus:     "pushed",
		PublishedAt:    now - 7200,
	})
	if err != nil {
		t.Fatalf("insert ev1: %v", err)
	}
	if ev1.EventCode == "" {
		t.Fatalf("expected generated event code")
	}

	_, err = st.InsertEvent(ctx, &model.IntelEvent{
		SourceName:     "qbitai",
		Category:       "ai",
		RawTitle:       "DeepSeek releases V4 open weights",
		ZhTitle:        "DeepSeek 开源新一代旗舰权重",
		Summary:        "推理成本再降一半，全面支持本地部署。",
		ImpactAnalysis: "利好端侧算力与应用层降本。",
		CanonicalURL:   "https://example.com/ds-1",
		Entities:       []string{"DeepSeek"},
		Score:          7,
		PushStatus:     "digested",
		PublishedAt:    now - 3600,
	})
	if err != nil {
		t.Fatalf("insert ev2: %v", err)
	}

	// Chinese substring search via CJK bigram tokens
	hits, err := st.ListEvents(ctx, model.EventFilter{
		Query: "反垄断 传票",
		Sort:  "relevance",
	})
	if err != nil || len(hits) != 1 || hits[0].EventCode != ev1.EventCode {
		t.Fatalf("expected 1 CJK FTS hit for ev1, got %d err=%v", len(hits), err)
	}

	// Entity timeline
	timeline, err := st.GetEntityTimeline(ctx, "nvda", 0, 0, 10)
	if err != nil || len(timeline) != 1 {
		t.Fatalf("expected 1 entity timeline hit, got %d err=%v", len(timeline), err)
	}

	// Bookmark + note sync
	note := "关键监管节点，持续跟踪"
	updated, err := st.SetBookmark(ctx, ev1.EventCode, true, &note)
	if err != nil || !updated.IsBookmarked || updated.UserNote != note {
		t.Fatalf("bookmark update failed: %+v err=%v", updated, err)
	}

	// Digest queue should contain score 7 event
	pending, err := st.PendingDigestItems(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("expected 1 pending digest item, got %d err=%v", len(pending), err)
	}

	// Budget hard cap circuit breaker
	_ = st.RecordAPIBudget(ctx, "twitterapi_io", 100, 150000, 1.50)
	_ = st.RecordAPIBudget(ctx, "twitterapi_io", 100, 140000, 1.40)
	budget, err := st.GetMonthlyBudget(ctx, 2.80)
	if err != nil || !budget.Tripped || budget.TotalUSD < 2.89 {
		t.Fatalf("expected budget tripped at >=2.80, got %+v err=%v", budget, err)
	}

	// Consistent VACUUM INTO hot backup
	backupPath := filepath.Join(t.TempDir(), "snap.db")
	if err := st.BackupToFile(ctx, backupPath); err != nil {
		t.Fatalf("backup failed: %v", err)
	}

	// Daily LLM token ledger aggregation
	if err := st.RecordLLMUsage(ctx, model.LLMUsageRecord{
		Provider:         "opencode-go",
		Model:            "deepseek-v4.1-flash",
		Scene:            "scoring",
		PromptTokens:     500,
		CompletionTokens: 120,
		TotalTokens:      620,
	}); err != nil {
		t.Fatalf("record llm usage 1: %v", err)
	}
	if err := st.RecordLLMUsage(ctx, model.LLMUsageRecord{
		Provider:         "opencode-go",
		Model:            "deepseek-v4-pro",
		Scene:            "ask_event",
		PromptTokens:     1200,
		CompletionTokens: 400,
		TotalTokens:      1600,
		Estimated:        true,
	}); err != nil {
		t.Fatalf("record llm usage 2: %v", err)
	}
	usage, err := st.GetLLMUsageSummary(ctx)
	if err != nil {
		t.Fatalf("get llm usage summary: %v", err)
	}
	if usage.Today.RequestCount != 2 || usage.Today.TotalTokens != 2220 || usage.Today.EstimatedCount != 1 {
		t.Fatalf("unexpected today usage: %+v", usage.Today)
	}
	if len(usage.Daily7d) != 7 || usage.Daily7d[6].TotalTokens != 2220 {
		t.Fatalf("expected 7 daily buckets ending with 2220, got %+v", usage.Daily7d)
	}
	if len(usage.ByScene) != 2 || len(usage.ByModel) != 2 {
		t.Fatalf("expected 2 scenes & 2 models, got scenes=%+v models=%+v", usage.ByScene, usage.ByModel)
	}
}
