package model

// RawItem represents a normalized incoming item from any collector before dedup and scoring.
type RawItem struct {
	SourceName       string `json:"source_name"`
	SourceType       string `json:"source_type"` // rss | json | twitter_api | finnhub_ws | status_atom | sec_atom
	ExternalID       string `json:"external_id"`
	Title            string `json:"title"`
	Summary          string `json:"summary"`
	URL              string `json:"url"`
	PublishedAt      int64  `json:"published_at"` // Unix seconds
	CategoryHint     string `json:"category_hint"`
	BypassRuleFilter bool   `json:"bypass_rule_filter"`
	TraditionalZh    bool   `json:"traditional_zh"`
	QuoteSnapshot    string `json:"quote_snapshot,omitempty"` // Optional JSON string for stock anomaly
	IsBaseline       bool   `json:"is_baseline,omitempty"`
}

// IntelEvent is the core stored intelligence record synced across backend, Hermes, and clients.
type IntelEvent struct {
	ID                 int64    `json:"id"`
	EventCode          string   `json:"event_code"`
	SourceName         string   `json:"source_name"`
	Category           string   `json:"category"` // finance | ai | bigtech | world
	RawTitle           string   `json:"raw_title"`
	ZhTitle            string   `json:"zh_title"`
	Summary            string   `json:"summary"`
	ImpactAnalysis     string   `json:"impact_analysis"`
	CanonicalURL       string   `json:"canonical_url"`
	Entities           []string `json:"entities"`
	QuoteSnapshot      string   `json:"quote_snapshot,omitempty"`
	Score              int      `json:"score"`
	Tier               string   `json:"tier"` // P1_CRITICAL | P1_HIGH | P2_DIGEST | P3_ARCHIVE
	IsBookmarked       bool     `json:"is_bookmarked"`
	UserNote           string   `json:"user_note"`
	PushStatus         string   `json:"push_status"`         // pushed | cooled_down | digested | silent_baseline | archived
	VerificationStatus string   `json:"verification_status"` // none | pending | confirmed | unverified | contradicted
	VerificationNote   string   `json:"verification_note"`
	FeishuMessageID    string   `json:"feishu_message_id,omitempty"`
	PublishedAt        int64    `json:"published_at"`
	CreatedAt          int64    `json:"created_at"`
	UpdatedAt          int64    `json:"updated_at"`
	Snippet            string   `json:"snippet,omitempty"`
}

// ScoreResult is the structured JSON returned by the L4 fast AI judge.
type ScoreResult struct {
	Score           int      `json:"score"`
	Category        string   `json:"category"`
	ZhTitle         string   `json:"zh_title"`
	Impact          string   `json:"impact"`
	Entities        []string `json:"entities"`
	PrimaryTopicKey string   `json:"primary_topic_key"`
}

// DigestItem represents a P2 event queued for the daily 21:00 executive digest.
type DigestItem struct {
	EventID     int64  `json:"event_id"`
	EventCode   string `json:"event_code"`
	Category    string `json:"category"`
	ZhTitle     string `json:"zh_title"`
	BulletPoint string `json:"bullet_point"`
	URL         string `json:"canonical_url"`
	Score       int    `json:"score"`
	PublishedAt int64  `json:"published_at"`
	CreatedAt   int64  `json:"created_at"`
}

// DailyDigest stores the generated daily executive digest markdown.
type DailyDigest struct {
	ID         int64  `json:"id"`
	DigestDate string `json:"digest_date"` // YYYY-MM-DD (UTC+8)
	Title      string `json:"title"`
	ContentMD  string `json:"content_md"`
	EventCount int    `json:"event_count"`
	CreatedAt  int64  `json:"created_at"`
}

// EventThread stores a user/assistant message in an event's AI follow-up thread.
type EventThread struct {
	ID        int64  `json:"id"`
	EventID   int64  `json:"event_id"`
	Role      string `json:"role"` // user | assistant
	ContentMD string `json:"content_md"`
	ModelUsed string `json:"model_used,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

// ClientDevice tracks a registered Windows or Android app instance.
type ClientDevice struct {
	DeviceID       string   `json:"device_id"`
	DeviceName     string   `json:"device_name"`
	Platform       string   `json:"platform"` // windows | android
	FCMToken       string   `json:"fcm_token,omitempty"`
	MinPushScore   int      `json:"min_push_score"`
	Categories     []string `json:"categories"`
	QuietHours     string   `json:"quiet_hours,omitempty"`
	LastSeenAt     int64    `json:"last_seen_at"`
	CreatedAt      int64    `json:"created_at"`
}

// SourceState records polling cursor and health state for each data source.
type SourceState struct {
	SourceName       string `json:"source_name"`
	SourceType       string `json:"source_type"`
	CursorValue      string `json:"cursor_value"`
	ETag             string `json:"etag"`
	LastModified     string `json:"last_modified"`
	LastSuccessAt    int64  `json:"last_success_at"`
	LastErrorAt      int64  `json:"last_error_at"`
	LastErrorMsg     string `json:"last_error_msg"`
	ConsecutiveFails int    `json:"consecutive_fails"`
	AlertedOffline   bool   `json:"alerted_offline"`
}

// BudgetRecord tracks monthly API usage and estimated spend per provider.
type BudgetRecord struct {
	MonthKey     string  `json:"month_key"`
	Provider     string  `json:"provider"`
	RequestCount int64   `json:"request_count"`
	UnitsUsed    int64   `json:"units_used"`
	EstimatedUSD float64 `json:"estimated_usd"`
	UpdatedAt    int64   `json:"updated_at"`
}

// BudgetSummary aggregates current month spend against hard caps.
type BudgetSummary struct {
	MonthKey      string         `json:"month_key"`
	TotalUSD      float64        `json:"total_usd"`
	MonthlyCapUSD float64        `json:"monthly_cap_usd"`
	Tripped       bool           `json:"tripped"`
	Providers     []BudgetRecord `json:"providers"`
}

// LLMUsageRecord represents a single LLM call's token accounting payload.
type LLMUsageRecord struct {
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	Scene            string `json:"scene"` // scoring | ask_event | ask_history | daily_digest
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	Estimated        bool   `json:"estimated"`
}

// LLMUsageBucket aggregates token counts for a period, day, scene, or model.
type LLMUsageBucket struct {
	Key              string `json:"key"`
	Label            string `json:"label,omitempty"`
	RequestCount     int64  `json:"request_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	EstimatedCount   int64  `json:"estimated_count"`
}

// QuotaWindow captures a rolling/weekly/monthly subscription quota window.
type QuotaWindow struct {
	Status           string  `json:"status"`
	UsedPercent      float64 `json:"used_percent"`
	RemainingPercent float64 `json:"remaining_percent"`
	ResetsAt         string  `json:"resets_at"`
}

// SubscriptionQuota exposes live remaining quota from OpenCode Go.
type SubscriptionQuota struct {
	Provider  string       `json:"provider"`
	Available bool         `json:"available"`
	FetchedAt int64        `json:"fetched_at"`
	Error     string       `json:"error,omitempty"`
	Rolling   *QuotaWindow `json:"rolling,omitempty"`
	Weekly    *QuotaWindow `json:"weekly,omitempty"`
	Monthly   *QuotaWindow `json:"monthly,omitempty"`
}

// LLMUsageSummary combines live subscription quota with local token breakdowns.
type LLMUsageSummary struct {
	Today   LLMUsageBucket     `json:"today"`
	Month   LLMUsageBucket     `json:"month"`
	Daily7d []LLMUsageBucket   `json:"daily_7d"`
	ByScene []LLMUsageBucket   `json:"by_scene"`
	ByModel []LLMUsageBucket   `json:"by_model"`
	Quota   *SubscriptionQuota `json:"quota,omitempty"`
}

// SystemOverview powers the client dashboard and CLI stats command.
type SystemOverview struct {
	TotalEvents       int64            `json:"total_events"`
	P1Events24h       int64            `json:"p1_events_24h"`
	BookmarkedEvents  int64            `json:"bookmarked_events"`
	TotalDigests      int64            `json:"total_digests"`
	DBSizeBytes       int64            `json:"db_size_bytes"`
	ActiveWSClients   int              `json:"active_ws_clients"`
	RegisteredDevices int              `json:"registered_devices"`
	Sources           []SourceState    `json:"sources"`
	Budget            BudgetSummary    `json:"budget"`
	LLMUsage          *LLMUsageSummary `json:"llm_usage,omitempty"`
	ServerTime        int64            `json:"server_time"`
}

// StreamMessage is broadcast over WebSocket /api/v1/stream to connected clients.
type StreamMessage struct {
	Type   string      `json:"type"` // event.created | event.verified | event.bookmarked | heartbeat
	SeqID  int64       `json:"seq_id,omitempty"`
	Event  *IntelEvent `json:"event,omitempty"`
	SentAt int64       `json:"sent_at"`
}

// EventFilter controls listing and historical querying.
type EventFilter struct {
	Query          string
	Categories     []string
	MinScore       int
	MaxScore       int
	FromTS         int64
	ToTS           int64
	Entity         string
	Source         string
	BookmarkedOnly bool
	VerifiedOnly   bool
	BeforeID       int64
	AfterID        int64
	Sort           string // time_desc | time_asc | score_desc | relevance
	Limit          int
}

// ModelSelection represents the runtime-configurable 3-slot LLM config isolated from Hermes chat.
type ModelSelection struct {
	Provider      string `json:"provider"`
	FastModel     string `json:"fast_model"`
	FallbackModel string `json:"fallback_model"`
	StrongModel   string `json:"strong_model"`
}

// ModelProviderCatalog exposes a configured subscription provider and its available model IDs.
type ModelProviderCatalog struct {
	Name    string   `json:"name"`
	BaseURL string   `json:"base_url,omitempty"`
	Models  []string `json:"models"`
}

// ModelSettingsResponse powers GET/PATCH /api/v1/settings/models.
type ModelSettingsResponse struct {
	Active    ModelSelection         `json:"active"`
	Providers []ModelProviderCatalog `json:"providers"`
}

// NotificationPolicy controls server-side alert routing (Feishu / FCM / verification / daily digest)
// configurable live from Windows/Android clients and persisted in SQLite app_settings.
type NotificationPolicy struct {
	MinPushScore           int      `json:"min_push_score"`            // 5..10 (default 8)
	Categories             []string `json:"categories"`                // finance | ai | bigtech | world
	EnableFeishu           bool     `json:"enable_feishu"`             // push cards to Feishu
	EnableVerificationPush bool     `json:"enable_verification_push"`  // attach 9+ fact-check updates
	EnableDailyDigest      bool     `json:"enable_daily_digest"`       // auto-generate 21:00 UTC+8 digest
}

// TierFromScore maps 1-10 score to severity tier.
func TierFromScore(score int) string {
	switch {
	case score >= 9:
		return "P1_CRITICAL"
	case score >= 8:
		return "P1_HIGH"
	case score >= 5:
		return "P2_DIGEST"
	default:
		return "P3_ARCHIVE"
	}
}
