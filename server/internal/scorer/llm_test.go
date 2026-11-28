package scorer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"intel-hub/internal/config"
	"intel-hub/internal/model"
)

func TestFetchSubscriptionQuotaAndCache(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/zen/go/v1/usage" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mu.Lock()
		calls++
		cur := calls
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"usage": map[string]any{
				"rolling": map[string]any{"status": "ok", "percent": 15, "resetsAt": "2026-11-28T16:00:00.000Z"},
				"weekly":  map[string]any{"status": "ok", "percent": 25, "resetsAt": "2026-11-30T00:00:00.000Z"},
				"monthly": map[string]any{"status": "ok", "percent": cur * 10, "resetsAt": "2026-12-20T13:56:36.000Z"},
			},
		})
	}))
	defer srv.Close()

	c := NewClient(config.LLMConfig{
		Provider: "opencode-go",
		BaseURL:  srv.URL + "/zen/go/v1",
		APIKey:   "test-key",
	})

	q1 := c.FetchSubscriptionQuota(context.Background(), false)
	if q1 == nil || !q1.Available {
		t.Fatalf("expected available quota, got %+v", q1)
	}
	if q1.Rolling.RemainingPercent != 85 || q1.Monthly.UsedPercent != 10 || q1.Monthly.RemainingPercent != 90 {
		t.Fatalf("unexpected quota values: %+v", q1)
	}

	// Cached hit should not call upstream again
	q2 := c.FetchSubscriptionQuota(context.Background(), false)
	if q2.Monthly.UsedPercent != 10 {
		t.Fatalf("expected cached monthly 10%%, got %.1f%%", q2.Monthly.UsedPercent)
	}

	// Forced refresh should bypass cache
	q3 := c.FetchSubscriptionQuota(context.Background(), true)
	if q3.Monthly.UsedPercent != 20 || q3.Monthly.RemainingPercent != 80 {
		t.Fatalf("expected refreshed monthly 20%%, got %+v", q3.Monthly)
	}
}

func TestStreamAskUsageHookFallbackWithoutStreamOptions(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if _, hasOpts := req["stream_options"]; hasOpts {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"unsupported stream_options"}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"测试结论\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":120,\"completion_tokens\":45,\"total_tokens\":165}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	c := NewClient(config.LLMConfig{
		Provider:    "opencode-go",
		BaseURL:     srv.URL,
		APIKey:      "k",
		StrongModel: "deepseek-v4.1-flash",
	})
	var got model.LLMUsageRecord
	c.SetUsageHook(func(rec model.LLMUsageRecord) { got = rec })

	full, mdl, err := c.StreamAskWithScene(context.Background(), "ask_history", "sys", "question", nil, func(string) error { return nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if full != "测试结论" || mdl != "deepseek-v4.1-flash" || attempts != 2 {
		t.Fatalf("unexpected stream result: full=%q mdl=%q attempts=%d", full, mdl, attempts)
	}
	if got.Scene != "ask_history" || got.PromptTokens != 120 || got.CompletionTokens != 45 || got.TotalTokens != 165 || got.Estimated {
		t.Fatalf("unexpected usage hook record: %+v", got)
	}
}
