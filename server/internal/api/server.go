package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"intel-hub/internal/config"
	"intel-hub/internal/model"
	"intel-hub/internal/notifier"
	"intel-hub/internal/pipeline"
	"intel-hub/internal/scorer"
	"intel-hub/internal/store"
)

// Server exposes REST, Full Historical Search, Entity Timeline, WebSocket Stream, and SSE AI Ask APIs.
type Server struct {
	cfg      *config.Config
	store    *store.Store
	hub      *Hub
	scorer   *scorer.Client
	enricher *pipeline.Enricher
	feishu   *notifier.FeishuNotifier
	fcm      *notifier.FCMNotifier

	policyMu    sync.RWMutex
	policy      model.NotificationPolicy
	authLimiter *authRateLimiter
}

func NewServer(cfg *config.Config, st *store.Store, hub *Hub, sc *scorer.Client, en *pipeline.Enricher, feishu *notifier.FeishuNotifier, fcm *notifier.FCMNotifier) *Server {
	minScore := cfg.Notify.FeishuMinScore
	if minScore <= 0 {
		minScore = 8
	}
	return &Server{
		cfg:         cfg,
		store:       st,
		hub:         hub,
		scorer:      sc,
		enricher:    en,
		feishu:      feishu,
		fcm:         fcm,
		authLimiter: newAuthRateLimiter(),
		policy: model.NotificationPolicy{
			MinPushScore:           minScore,
			Categories:             []string{"finance", "ai", "bigtech", "world"},
			EnableFeishu:           true,
			EnableVerificationPush: true,
			EnableDailyDigest:      true,
		},
	}
}

func (s *Server) GetNotificationPolicy() model.NotificationPolicy {
	s.policyMu.RLock()
	defer s.policyMu.RUnlock()
	p := s.policy
	p.Categories = append([]string(nil), s.policy.Categories...)
	return p
}

func (s *Server) SetNotificationPolicy(p model.NotificationPolicy) {
	if p.MinPushScore < 1 {
		p.MinPushScore = 8
	}
	if p.MinPushScore > 10 {
		p.MinPushScore = 10
	}
	if len(p.Categories) == 0 {
		p.Categories = []string{"finance", "ai", "bigtech", "world"}
	}
	s.policyMu.Lock()
	s.policy = p
	s.policyMu.Unlock()
}

// GenerateTodayDigest compiles today's P1 + pending P2 items into daily_digests and notifies Feishu.
func (s *Server) GenerateTodayDigest(ctx context.Context) (model.DailyDigest, error) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*3600)
	nowBJ := time.Now().In(shanghai)
	dateStr := nowBJ.Format("2006-01-02")
	sinceTS := time.Now().Add(-24 * time.Hour).Unix()

	p1Events, _ := s.store.ListEvents(ctx, model.EventFilter{
		MinScore: 8,
		FromTS:   sinceTS,
		Sort:     "score_desc",
		Limit:    25,
	})
	pendingP2, _ := s.store.PendingDigestItems(ctx)
	var recentP2 []model.DigestItem
	for _, it := range pendingP2 {
		if it.PublishedAt >= sinceTS || it.CreatedAt >= sinceTS {
			recentP2 = append(recentP2, it)
		}
	}
	if len(recentP2) == 0 && len(pendingP2) > 0 {
		recentP2 = pendingP2
	}
	if len(p1Events) == 0 && len(recentP2) == 0 {
		return model.DailyDigest{}, fmt.Errorf("过去 24 小时暂无可汇总的 5 分以上情报")
	}

	md, modelUsed, err := s.scorer.GenerateDailyDigest(ctx, dateStr, p1Events, recentP2)
	if err != nil {
		return model.DailyDigest{}, err
	}
	digest := model.DailyDigest{
		DigestDate: dateStr,
		Title:      "今日情报内参",
		ContentMD:  md,
		EventCount: len(p1Events) + len(recentP2),
		CreatedAt:  time.Now().Unix(),
	}
	if err := s.store.SaveDailyDigest(ctx, digest); err != nil {
		return model.DailyDigest{}, err
	}
	_, _ = s.store.AckDigestItems(ctx, time.Now().Unix())
	log.Printf("[digest] generated %s (%d items) via %s", dateStr, digest.EventCount, modelUsed)

	policy := s.GetNotificationPolicy()
	if policy.EnableFeishu && s.feishu != nil && s.feishu.Enabled() {
		go func(d model.DailyDigest) {
			nctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := s.feishu.SendDigestCard(nctx, d); err != nil {
				log.Printf("[feishu] digest card failed: %v", err)
			}
		}(digest)
	}
	return digest, nil
}

// OnEventCreated implements pipeline.EventSink for real-time delivery to WS clients, Android FCM, and Feishu.
func (s *Server) OnEventCreated(ctx context.Context, ev *model.IntelEvent) {
	s.hub.Broadcast(model.StreamMessage{
		Type:  "event.created",
		SeqID: ev.ID,
		Event: ev,
	})

	if ev.PushStatus == "cooled_down" {
		return
	}

	policy := s.GetNotificationPolicy()

	// Feishu notification governed by live NotificationPolicy
	if policy.EnableFeishu && ev.Score >= policy.MinPushScore && categoryAllowed(policy.Categories, ev.Category) && s.feishu != nil && s.feishu.Enabled() {
		go func(e model.IntelEvent) {
			nctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			msgID, err := s.feishu.SendEventCard(nctx, &e)
			if err != nil {
				log.Printf("[feishu] send failed: %v", err)
				return
			}
			if msgID != "" {
				_ = s.store.SetFeishuMessageID(nctx, e.ID, msgID)
			}
		}(*ev)
	}

	// Android FCM wake-up push
	if ev.Score >= policy.MinPushScore && s.fcm != nil && s.fcm.Enabled() {
		go func(e model.IntelEvent) {
			nctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			devices, err := s.store.ListFCMDevices(nctx)
			if err != nil {
				return
			}
			for _, dev := range devices {
				if e.Score < dev.MinPushScore {
					continue
				}
				if !categoryAllowed(dev.Categories, e.Category) {
					continue
				}
				if err := s.fcm.SendToDevice(nctx, dev, &e); err != nil {
					log.Printf("[fcm] device=%s err=%v", dev.DeviceID, err)
				}
			}
		}(*ev)
	}
}

// OnEventVerified implements pipeline.EventSink when 9+ breaking fact-checking completes.
func (s *Server) OnEventVerified(ctx context.Context, ev *model.IntelEvent) {
	s.hub.Broadcast(model.StreamMessage{
		Type:  "event.verified",
		SeqID: ev.ID,
		Event: ev,
	})
	policy := s.GetNotificationPolicy()
	if policy.EnableFeishu && policy.EnableVerificationPush && s.feishu != nil && s.feishu.Enabled() && ev.Score >= policy.MinPushScore {
		go func(e model.IntelEvent) {
			nctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = s.feishu.SendVerificationUpdate(nctx, &e)
		}(*ev)
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/auth/status", s.handleAuthStatus)
	mux.HandleFunc("POST /api/v1/auth/register", s.handleAuthRegister)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleAuthLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", s.auth(s.handleAuthLogout))
	mux.HandleFunc("GET /api/v1/auth/me", s.auth(s.handleAuthMe))
	mux.HandleFunc("PUT /api/v1/auth/preferences", s.auth(s.handleUpdatePreferences))
	mux.HandleFunc("POST /api/v1/auth/change-password", s.auth(s.handleChangePassword))
	mux.HandleFunc("GET /api/v1/events", s.auth(s.handleListEvents))
	mux.HandleFunc("GET /api/v1/events/search", s.auth(s.handleSearchEvents))
	mux.HandleFunc("GET /api/v1/events/{code}", s.auth(s.handleGetEvent))
	mux.HandleFunc("PATCH /api/v1/events/{code}/bookmark", s.auth(s.handleBookmark))
	mux.HandleFunc("POST /api/v1/events/{code}/ask", s.auth(s.handleAskEvent))
	mux.HandleFunc("POST /api/v1/history/ask", s.auth(s.handleAskHistory))
	mux.HandleFunc("GET /api/v1/entities/{name}/timeline", s.auth(s.handleEntityTimeline))
	mux.HandleFunc("GET /api/v1/digests", s.auth(s.handleListDigests))
	mux.HandleFunc("POST /api/v1/digests/generate", s.auth(s.handleGenerateDigest))
	mux.HandleFunc("POST /api/v1/devices/sync", s.auth(s.handleSyncDevice))
	mux.HandleFunc("POST /api/v1/devices/test-fcm", s.auth(s.handleTestFCM))
	mux.HandleFunc("GET /api/v1/system/overview", s.auth(s.handleSystemOverview))
	mux.HandleFunc("GET /api/v1/settings/models", s.auth(s.handleGetModels))
	mux.HandleFunc("PATCH /api/v1/settings/models", s.auth(s.handleUpdateModels))
	mux.HandleFunc("GET /api/v1/settings/notifications", s.auth(s.handleGetNotificationPolicy))
	mux.HandleFunc("PATCH /api/v1/settings/notifications", s.auth(s.handleUpdateNotificationPolicy))
	mux.HandleFunc("GET /api/v1/stream", s.auth(s.handleStream))

	return withCORS(mux)
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		expected := strings.TrimSpace(s.cfg.Server.APIToken)
		tok := strings.TrimPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ")
		if tok == "" {
			tok = strings.TrimSpace(r.URL.Query().Get("token"))
		}

		// 1. Check user session token first (ihs_...)
		if strings.HasPrefix(tok, "ihs_") {
			sess, err := s.store.ValidateSessionToken(r.Context(), tok)
			if err == nil && sess != nil {
				ctx := context.WithValue(r.Context(), authContextKey{}, authPrincipal{
					UserID:   sess.UserID,
					Username: sess.Username,
					IsLegacy: false,
					RawToken: tok,
				})
				next(w, r.WithContext(ctx))
				return
			}
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "登录会话已失效，请重新登录"})
			return
		}

		// 2. Fallback: legacy static INTEL_API_TOKEN (keeps Hermes CLI, sentinel, and old clients working)
		if expected == "" {
			ctx := context.WithValue(r.Context(), authContextKey{}, authPrincipal{IsLegacy: true})
			next(w, r.WithContext(ctx))
			return
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(expected)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		ctx := context.WithValue(r.Context(), authContextKey{}, authPrincipal{IsLegacy: true, RawToken: tok})
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"service": "intel-hub",
		"ts":      time.Now().Unix(),
	})
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	f := parseFilter(r)
	events, err := s.store.ListEvents(r.Context(), f)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if events == nil {
		events = []model.IntelEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": events, "count": len(events)})
}

func (s *Server) handleSearchEvents(w http.ResponseWriter, r *http.Request) {
	f := parseFilter(r)
	if f.Sort == "" && f.Query != "" {
		f.Sort = "relevance"
	}
	events, err := s.store.ListEvents(r.Context(), f)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if events == nil {
		events = []model.IntelEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": events, "count": len(events), "query": f.Query})
}

func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	ev, err := s.store.GetEventByCode(r.Context(), code)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "event not found"})
		return
	}
	threads, _ := s.store.GetEventThreads(r.Context(), ev.ID)
	if threads == nil {
		threads = []model.EventThread{}
	}
	var related []model.IntelEvent
	if len(ev.Entities) > 0 {
		if tl, err := s.store.GetEntityTimeline(r.Context(), ev.Entities[0], 0, 0, 8); err == nil {
			for _, item := range tl {
				if item.ID != ev.ID {
					related = append(related, item)
				}
			}
		}
	}
	if related == nil {
		related = []model.IntelEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"event":   ev,
		"threads": threads,
		"related": related,
	})
}

func (s *Server) handleBookmark(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	var req struct {
		Bookmarked bool    `json:"is_bookmarked"`
		UserNote   *string `json:"user_note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	ev, err := s.store.SetBookmark(r.Context(), code, req.Bookmarked, req.UserNote)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	s.hub.Broadcast(model.StreamMessage{
		Type:  "event.bookmarked",
		SeqID: ev.ID,
		Event: ev,
	})
	writeJSON(w, http.StatusOK, ev)
}

func (s *Server) handleEntityTimeline(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	fromTS, _ := strconv.ParseInt(r.URL.Query().Get("from_ts"), 10, 64)
	toTS, _ := strconv.ParseInt(r.URL.Query().Get("to_ts"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 60
	}
	items, err := s.store.GetEntityTimeline(r.Context(), name, fromTS, toTS, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if items == nil {
		items = []model.IntelEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entity": strings.ToUpper(name), "items": items, "count": len(items)})
}

func (s *Server) handleListDigests(w http.ResponseWriter, r *http.Request) {
	month := strings.TrimSpace(r.URL.Query().Get("month"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.store.ListDigests(r.Context(), month, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if items == nil {
		items = []model.DailyDigest{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (s *Server) handleGenerateDigest(w http.ResponseWriter, r *http.Request) {
	digest, err := s.GenerateTodayDigest(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, digest)
}

func (s *Server) handleGetNotificationPolicy(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.GetNotificationPolicy())
}

func (s *Server) handleUpdateNotificationPolicy(w http.ResponseWriter, r *http.Request) {
	var p model.NotificationPolicy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	s.SetNotificationPolicy(p)
	active := s.GetNotificationPolicy()
	raw, _ := json.Marshal(active)
	if err := s.store.SetSetting(r.Context(), "notification_policy", string(raw)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("[settings] updated notification policy: min_score=%d categories=%v feishu=%v verify=%v daily_digest=%v",
		active.MinPushScore, active.Categories, active.EnableFeishu, active.EnableVerificationPush, active.EnableDailyDigest)
	writeJSON(w, http.StatusOK, active)
}

func (s *Server) handleSyncDevice(w http.ResponseWriter, r *http.Request) {
	var dev model.ClientDevice
	if err := json.NewDecoder(r.Body).Decode(&dev); err != nil || dev.DeviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device_id is required"})
		return
	}
	if err := s.store.UpsertDevice(r.Context(), dev); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"device_id":   dev.DeviceID,
		"fcm_enabled": s.fcm != nil && s.fcm.Enabled(),
	})
}

func (s *Server) handleTestFCM(w http.ResponseWriter, r *http.Request) {
	if s.fcm == nil || !s.fcm.Enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "FCM 服务端未启用（未检测到 FCM_SERVICE_ACCOUNT_PATH）"})
		return
	}
	var req struct {
		DeviceID string `json:"device_id"`
		DelaySec int    `json:"delay_sec"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.DelaySec < 0 {
		req.DelaySec = 0
	}
	if req.DelaySec > 15 {
		req.DelaySec = 15
	}
	devices, err := s.store.ListFCMDevices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var targets []model.ClientDevice
	for _, d := range devices {
		if req.DeviceID == "" || d.DeviceID == req.DeviceID {
			targets = append(targets, d)
		}
	}
	if len(targets) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "尚未注册可用的 Android FCM 设备 Token，请先在手机端保存连接"})
		return
	}
	testEv := &model.IntelEvent{
		ID:             0,
		EventCode:      "EVT-FCM-TEST",
		Category:       "ai",
		ZhTitle:        "FCM 息屏零耗电推送链路验证成功",
		ImpactAnalysis: "服务端已通过 Google FCM HTTP v1 通道高优直达您的手机通知栏，即使划掉后台或锁屏也能秒级唤醒。",
		Score:          9,
		Tier:           "P1_CRITICAL",
	}
	if req.DelaySec > 0 {
		go func(list []model.ClientDevice, delay int) {
			time.Sleep(time.Duration(delay) * time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			for _, d := range list {
				if err := s.fcm.SendToDevice(ctx, d, testEv); err != nil {
					log.Printf("[fcm:test] device=%s err=%v", d.DeviceID, err)
				}
			}
		}(targets, req.DelaySec)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "scheduled_delay_sec": req.DelaySec, "devices": len(targets)})
		return
	}
	sent := 0
	var lastErr string
	for _, d := range targets {
		if err := s.fcm.SendToDevice(r.Context(), d, testEv); err != nil {
			lastErr = err.Error()
			log.Printf("[fcm:test] device=%s err=%v", d.DeviceID, err)
		} else {
			sent++
		}
	}
	if sent == 0 {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "FCM 发送失败: " + lastErr})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sent": sent})
}

func (s *Server) handleSystemOverview(w http.ResponseWriter, r *http.Request) {
	ov, err := s.store.GetSystemOverview(r.Context(), s.cfg.Budget.MonthlyCapUSD, s.hub.ActiveCount())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	forceQuota := r.URL.Query().Get("refresh_quota") == "1" || r.URL.Query().Get("refresh_quota") == "true"
	if s.scorer != nil {
		if ov.LLMUsage == nil {
			ov.LLMUsage = &model.LLMUsageSummary{}
		}
		ov.LLMUsage.Quota = s.scorer.FetchSubscriptionQuota(r.Context(), forceQuota)
	}
	writeJSON(w, http.StatusOK, ov)
}

func (s *Server) handleGetModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, model.ModelSettingsResponse{
		Active:    s.scorer.GetSelection(),
		Providers: s.scorer.ListProviders(),
	})
}

func (s *Server) handleUpdateModels(w http.ResponseWriter, r *http.Request) {
	var sel model.ModelSelection
	if err := json.NewDecoder(r.Body).Decode(&sel); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	s.scorer.UpdateSelection(sel)
	active := s.scorer.GetSelection()
	raw, _ := json.Marshal(active)
	if err := s.store.SetSetting(r.Context(), "llm_model_config", string(raw)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("[settings] hot-updated isolated LLM models: provider=%s fast=%s fallback=%s strong=%s",
		active.Provider, active.FastModel, active.FallbackModel, active.StrongModel)
	writeJSON(w, http.StatusOK, model.ModelSettingsResponse{
		Active:    active,
		Providers: s.scorer.ListProviders(),
	})
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	afterID, _ := strconv.ParseInt(r.URL.Query().Get("after_id"), 10, 64)
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Allow desktop/mobile native clients across Cloudflare edge
	})
	if err != nil {
		return
	}
	var replay []model.IntelEvent
	if afterID > 0 {
		replay, _ = s.store.ListEvents(r.Context(), model.EventFilter{
			AfterID: afterID,
			Limit:   50,
		})
	}
	s.hub.ServeConn(r.Context(), conn, replay)
}

// handleAskEvent streams a deep-dive answer grounded in the target event + optional live web search.
// Flushes SSE headers and keepalive heartbeats immediately BEFORE search/LLM so mobile/Cloudflare connections never drop.
func (s *Server) handleAskEvent(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	ev, err := s.store.GetEventByCode(r.Context(), code)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "event not found"})
		return
	}
	var req struct {
		Question  string `json:"question"`
		WebSearch bool   `json:"web_search"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Question) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "question required"})
		return
	}

	history, _ := s.store.GetEventThreads(r.Context(), ev.ID)

	s.streamSSEWithPrep(w, r, "ask_event", func(emitStatus func(string)) (string, string, []model.EventThread, func(string, string)) {
		var searchNotes string
		if req.WebSearch && s.enricher != nil && s.enricher.Enabled() {
			emitStatus("正在联网检索最新信源…")
			sctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
			hits, serr := s.enricher.SearchNews(sctx, ev.ZhTitle+" "+req.Question)
			cancel()
			if serr == nil && len(hits) > 0 {
				var lines []string
				for _, h := range hits {
					lines = append(lines, fmt.Sprintf("- [%s](%s): %s", h.Title, h.URL, h.Snippet))
				}
				searchNotes = "\n\n【实时联网检索补充】\n" + strings.Join(lines, "\n")
			}
		}

		emitStatus("正在连接模型深度研判…")
		sysPrompt := fmt.Sprintf(`你是用户的专属金融与科技深度情报官。请基于以下已入库的突发情报给出专业、克制、结构化的中文深度分析（使用 Markdown）。
事件编号：%s
中文标题：%s
原始标题：%s
所属领域：%s | 评分：%d/10
正文摘要：%s
AI 初评影响：%s
多源核验状态：%s (%s)
原文链接：%s%s`,
			ev.EventCode, ev.ZhTitle, ev.RawTitle, ev.Category, ev.Score, ev.Summary, ev.ImpactAnalysis,
			ev.VerificationStatus, ev.VerificationNote, ev.CanonicalURL, searchNotes)

		onDone := func(full, modelUsed string) {
			_ = s.store.AppendThreadMessage(context.Background(), ev.ID, "user", req.Question, "")
			_ = s.store.AppendThreadMessage(context.Background(), ev.ID, "assistant", full, modelUsed)
		}
		return sysPrompt, req.Question, history, onDone
	})
}

// handleAskHistory runs Historical RAG across the permanent SQLite archive and streams a cited report.
func (s *Server) handleAskHistory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Question string `json:"question"`
		Query    string `json:"query"`
		FromTS   int64  `json:"from_ts"`
		ToTS     int64  `json:"to_ts"`
		Entity   string `json:"entity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Question) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "question required"})
		return
	}

	s.streamSSEWithPrep(w, r, "ask_history", func(emitStatus func(string)) (string, string, []model.EventThread, func(string, string)) {
		emitStatus("正在检索永久历史情报库…")
		searchQ := strings.TrimSpace(req.Query)
		if searchQ == "" {
			searchQ = req.Question
		}
		events, _ := s.store.ListEvents(r.Context(), model.EventFilter{
			Query:  searchQ,
			Entity: req.Entity,
			FromTS: req.FromTS,
			ToTS:   req.ToTS,
			Sort:   "relevance",
			Limit:  25,
		})
		if len(events) == 0 {
			events, _ = s.store.ListEvents(r.Context(), model.EventFilter{
				Entity: req.Entity,
				FromTS: req.FromTS,
				ToTS:   req.ToTS,
				Sort:   "time_desc",
				Limit:  20,
			})
		}

		shanghai := time.FixedZone("Asia/Shanghai", 8*3600)
		var ctxLines []string
		for _, ev := range events {
			dt := time.Unix(ev.PublishedAt, 0).In(shanghai).Format("2006-01-02 15:04")
			ctxLines = append(ctxLines, fmt.Sprintf("- [%s] (%s | %d/10 | %s) %s — %s",
				ev.EventCode, dt, ev.Score, ev.Category, ev.ZhTitle, ev.ImpactAnalysis))
		}
		emitStatus("正在生成跨周期历史复盘…")
		sysPrompt := "你是历史情报复盘分析师。请严格基于以下从用户永久 SQLite 情报库中检索出的历史事件记录回答问题，并在关键结论后标注对应事件编号（如 `[#EVT-...]`）以便溯源。\n\n【历史情报库命中记录】\n" + strings.Join(ctxLines, "\n")
		return sysPrompt, req.Question, nil, nil
	})
}

func (s *Server) streamSSEWithPrep(
	w http.ResponseWriter,
	r *http.Request,
	scene string,
	prepare func(emitStatus func(string)) (sysPrompt, userPrompt string, history []model.EventThread, onDone func(full, modelUsed string)),
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var writeMu sync.Mutex
	writeRaw := func(raw string) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_, err := io.WriteString(w, raw)
		if err == nil {
			flusher.Flush()
		}
		return err
	}
	writeData := func(v any) error {
		chunk, _ := json.Marshal(v)
		return writeRaw(fmt.Sprintf("data: %s\n\n", chunk))
	}

	// Immediately flush initial SSE comment so Cloudflare Tunnel / mobile NAT locks the stream open.
	_ = writeRaw(": connected\n\n")

	// 10s heartbeat ticker prevents idle connection drops during web search or slow LLM TTFT.
	hbStop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-hbStop:
				return
			case <-r.Context().Done():
				return
			case <-ticker.C:
				_ = writeRaw(": keepalive\n\n")
			}
		}
	}()
	defer close(hbStop)

	sysPrompt, userPrompt, history, onDone := prepare(func(status string) {
		_ = writeData(map[string]string{"status": status})
	})

	full, modelUsed, err := s.scorer.StreamAskWithScene(r.Context(), scene, sysPrompt, userPrompt, history, func(delta string) error {
		return writeData(map[string]string{"delta": delta})
	})
	if err != nil {
		log.Printf("[ask] stream error (model=%s): %v", modelUsed, err)
		_ = writeData(map[string]string{"error": err.Error()})
		return
	}
	if onDone != nil && full != "" {
		onDone(full, modelUsed)
	}
	_ = writeData(map[string]any{"done": true, "model": modelUsed})
}

func parseFilter(r *http.Request) model.EventFilter {
	q := r.URL.Query()
	var cats []string
	if raw := strings.TrimSpace(q.Get("categories")); raw != "" {
		for _, c := range strings.Split(raw, ",") {
			if c = strings.TrimSpace(c); c != "" {
				cats = append(cats, c)
			}
		}
	} else if single := strings.TrimSpace(q.Get("category")); single != "" {
		cats = []string{single}
	}
	minScore, _ := strconv.Atoi(q.Get("min_score"))
	maxScore, _ := strconv.Atoi(q.Get("max_score"))
	fromTS, _ := strconv.ParseInt(q.Get("from_ts"), 10, 64)
	toTS, _ := strconv.ParseInt(q.Get("to_ts"), 10, 64)
	beforeID, _ := strconv.ParseInt(q.Get("before_id"), 10, 64)
	afterID, _ := strconv.ParseInt(q.Get("after_id"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))

	return model.EventFilter{
		Query:          strings.TrimSpace(q.Get("q")),
		Categories:     cats,
		MinScore:       minScore,
		MaxScore:       maxScore,
		FromTS:         fromTS,
		ToTS:           toTS,
		Entity:         strings.TrimSpace(q.Get("entity")),
		Source:         strings.TrimSpace(q.Get("source")),
		BookmarkedOnly: q.Get("bookmarked_only") == "1" || q.Get("bookmarked_only") == "true",
		VerifiedOnly:   q.Get("verified_only") == "1" || q.Get("verified_only") == "true",
		BeforeID:       beforeID,
		AfterID:        afterID,
		Sort:           strings.TrimSpace(q.Get("sort")),
		Limit:          limit,
	}
}

func categoryAllowed(cats []string, target string) bool {
	if len(cats) == 0 {
		return true
	}
	for _, c := range cats {
		if c == target {
			return true
		}
	}
	return false
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
