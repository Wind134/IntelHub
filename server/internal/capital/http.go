package capital

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"intel-hub/internal/config"
	"intel-hub/internal/store"
)

//go:embed all:webdist
var embeddedWebFS embed.FS

const sessionCookieName = "infohub_capital_sid"

// WebServer 提供独立端口（默认 127.0.0.1:8788）的三地资金流向 Web 看板与严格鉴权 API。
type WebServer struct {
	cfg     *config.Config
	store   *store.Store
	engine  *Engine
	limiter *loginLimiter
}

type loginLimiter struct {
	mu      sync.Mutex
	buckets map[string]*struct {
		fails       int
		windowStart time.Time
		lockedUntil time.Time
	}
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{buckets: make(map[string]*struct {
		fails       int
		windowStart time.Time
		lockedUntil time.Time
	})}
}

func (l *loginLimiter) allow(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[ip]
	if !ok {
		return true, 0
	}
	now := time.Now()
	if now.Before(b.lockedUntil) {
		return false, time.Until(b.lockedUntil)
	}
	if now.Sub(b.windowStart) > 15*time.Minute {
		delete(l.buckets, ip)
	}
	return true, 0
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[ip]
	if !ok || now.Sub(b.windowStart) > 15*time.Minute {
		b = &struct {
			fails       int
			windowStart time.Time
			lockedUntil time.Time
		}{windowStart: now}
		l.buckets[ip] = b
	}
	b.fails++
	if b.fails >= 5 {
		b.lockedUntil = now.Add(15 * time.Minute)
	}
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	delete(l.buckets, ip)
	l.mu.Unlock()
}

func NewWebServer(cfg *config.Config, st *store.Store, engine *Engine) *WebServer {
	return &WebServer{
		cfg:     cfg,
		store:   st,
		engine:  engine,
		limiter: newLoginLimiter(),
	}
}

func (s *WebServer) Handler() http.Handler {
	mux := http.NewServeMux()

	// 公共健康与鉴权入口
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "capital-flow-web"})
	})
	mux.HandleFunc("GET /api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/me", s.requireAuth(s.handleMe))

	// 严格受登录态保护的三地资金流向 API（未登录一律 401 拒绝）
	mux.HandleFunc("GET /api/capital/overview", s.requireAuth(s.handleOverview))
	mux.HandleFunc("GET /api/capital/cn/etfs", s.requireAuth(s.handleCNETFs))
	mux.HandleFunc("GET /api/capital/cn/etfs/{code}", s.requireAuth(s.handleCNETFDetail))
	mux.HandleFunc("GET /api/capital/us/overview", s.requireAuth(s.handleUSOverview))
	mux.HandleFunc("GET /api/capital/us/institutions/{cik}", s.requireAuth(s.handleUSInstitution))
	mux.HandleFunc("GET /api/capital/us/stocks/{symbol}", s.requireAuth(s.handleUSStock))
	mux.HandleFunc("GET /api/capital/hk/southbound", s.requireAuth(s.handleHKSouthbound))
	mux.HandleFunc("GET /api/capital/hk/stocks/{code}", s.requireAuth(s.handleHKStockDetail))
	mux.HandleFunc("GET /api/capital/watchlist", s.requireAuth(s.handleListWatchlist))
	mux.HandleFunc("POST /api/capital/watchlist", s.requireAuth(s.handleAddWatchlist))
	mux.HandleFunc("DELETE /api/capital/watchlist/{market}/{code}", s.requireAuth(s.handleDeleteWatchlist))
	mux.HandleFunc("POST /api/capital/sync", s.requireAuth(s.handleTriggerSync))

	// 静态前端资源（SPA fallback，前端路由内强制未登录仅展示登录页，且所有数据接口严格 401）
	sub, err := fs.Sub(embeddedWebFS, "webdist")
	if err == nil {
		fileSrv := http.FileServer(http.FS(sub))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
				return
			}
			clean := strings.TrimPrefix(r.URL.Path, "/")
			if clean == "" {
				clean = "index.html"
			}
			if f, err := sub.Open(clean); err == nil {
				f.Close()
				fileSrv.ServeHTTP(w, r)
				return
			}
			// SPA History fallback
			idx, err := fs.ReadFile(sub, "index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(idx)
		})
	}
	return mux
}

type sessCtxKey struct{}

func (s *WebServer) extractToken(r *http.Request) string {
	if c, err := r.Cookie(sessionCookieName); err == nil && strings.TrimSpace(c.Value) != "" {
		return strings.TrimSpace(c.Value)
	}
	return strings.TrimPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ")
}

func (s *WebServer) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := s.extractToken(r)
		if tok == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "请先登录后访问"})
			return
		}
		sess, err := s.store.ValidateSessionToken(r.Context(), tok)
		if err != nil || sess == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "登录会话已失效，请重新登录"})
			return
		}
		ctx := context.WithValue(r.Context(), sessCtxKey{}, sess)
		next(w, r.WithContext(ctx))
	}
}

func (s *WebServer) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	n, _ := s.store.CountUsers(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"has_users":        n > 0,
		"requires_setup":   n == 0,
		"setup_key_needed": strings.TrimSpace(s.cfg.Server.APIToken) != "",
	})
}

func (s *WebServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := reqIP(r)
	if ok, wait := s.limiter.allow(ip); !ok {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "登录尝试过于频繁，请 " + wait.Round(time.Second).String() + " 后再试"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效请求"})
		return
	}
	user, err := s.store.GetUserByUsername(r.Context(), req.Username)
	if err != nil || !store.VerifyPassword(req.Password, user.PasswordHash) {
		s.limiter.fail(ip)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "用户名或密码错误"})
		return
	}
	tok, exp, err := s.store.CreateSession(r.Context(), user.ID, "Capital Flow Web", "web")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "创建会话失败"})
		return
	}
	s.limiter.reset(ip)
	setSessionCookie(w, r, tok, exp)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": user})
}

func (s *WebServer) handleRegister(w http.ResponseWriter, r *http.Request) {
	ip := reqIP(r)
	if ok, wait := s.limiter.allow(ip); !ok {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "操作过于频繁，请 " + wait.Round(time.Second).String() + " 后再试"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		SetupKey string `json:"setup_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效请求"})
		return
	}
	expected := strings.TrimSpace(s.cfg.Server.APIToken)
	if expected != "" && subtle.ConstantTimeCompare([]byte(strings.TrimSpace(req.SetupKey)), []byte(expected)) != 1 {
		s.limiter.fail(ip)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "注册邀请密钥（服务器 INTEL_API_TOKEN）校验失败"})
		return
	}
	user, err := s.store.CreateUser(r.Context(), req.Username, req.Username, req.Password)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	tok, exp, err := s.store.CreateSession(r.Context(), user.ID, "Capital Flow Web", "web")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "创建会话失败"})
		return
	}
	s.limiter.reset(ip)
	setSessionCookie(w, r, tok, exp)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": user})
}

func (s *WebServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	if tok := s.extractToken(r); tok != "" {
		_ = s.store.RevokeSessionToken(r.Context(), tok)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *WebServer) handleMe(w http.ResponseWriter, r *http.Request) {
	sess := r.Context().Value(sessCtxKey{}).(*store.UserSession)
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"user": map[string]any{
			"id":       sess.UserID,
			"username": sess.Username,
		},
	})
}

func (s *WebServer) handleOverview(w http.ResponseWriter, r *http.Request) {
	statuses, _ := s.engine.Repo.ListSyncStatuses(r.Context())
	cnList, _ := s.engine.Repo.ListCNETFSummaries(r.Context())
	usFilings, _ := s.engine.Repo.ListLatestUSFilings(r.Context())
	hkSB, hkDate, _ := s.engine.Repo.ListLatestHKSouthbound(r.Context(), 50)

	cnGovTotalYi := 0.0
	cnFlow1DYi := 0.0
	cnSurgeCount := 0
	for _, e := range cnList {
		cnGovTotalYi += e.GovMTMValueYi
		cnFlow1DYi += e.Flow1DYi
		if e.VolumeSignal == "intraday_surge" || e.VolumeSignal == "confirmed_inflow" {
			cnSurgeCount++
		}
	}
	usTotalUSD := 0.0
	for _, f := range usFilings {
		usTotalUSD += f.TotalValueUSD
	}
	hkNet1DYi := 0.0
	for _, h := range hkSB {
		hkNet1DYi += h.NetFlowHKDYi
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"statuses":        statuses,
		"progress":        s.engine.SnapshotProgress(),
		"tushare_enabled": s.engine.CN.ts.Enabled(),
		"kpi": map[string]any{
			"cn_gov_total_rmb_yi": cnGovTotalYi,
			"cn_flow_1d_rmb_yi":   cnFlow1DYi,
			"cn_surge_count":      cnSurgeCount,
			"cn_etf_count":        len(cnList),
			"us_inst_count":       len(usFilings),
			"us_total_aum_usd":    usTotalUSD,
			"hk_latest_date":      hkDate,
			"hk_top50_flow_hkd":   hkNet1DYi,
		},
	})
}

func (s *WebServer) handleCNETFs(w http.ResponseWriter, r *http.Request) {
	items, err := s.engine.Repo.ListCNETFSummaries(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"currency": "RMB", "items": items})
}

func (s *WebServer) handleCNETFDetail(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.PathValue("code"))
	series, err := s.engine.Repo.GetCNETFSeries(r.Context(), code, 90)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	holders, _ := s.engine.Repo.GetCNETFHolders(r.Context(), code)
	writeJSON(w, http.StatusOK, map[string]any{
		"code":     code,
		"currency": "RMB",
		"series":   series,
		"holders":  holders,
	})
}

func (s *WebServer) handleUSOverview(w http.ResponseWriter, r *http.Request) {
	filings, _ := s.engine.Repo.ListLatestUSFilings(r.Context())
	consensus, _ := s.engine.Repo.ListUSConsensusStocks(r.Context(), 80)
	writeJSON(w, http.StatusOK, map[string]any{
		"currency":     "USD",
		"institutions": filings,
		"consensus":    consensus,
	})
}

func (s *WebServer) handleUSInstitution(w http.ResponseWriter, r *http.Request) {
	cik := padCIK(r.PathValue("cik"))
	reportDate := strings.TrimSpace(r.URL.Query().Get("report_date"))
	assetClass := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("asset_class")))
	if assetClass == "" {
		assetClass = "EQUITY"
	}
	holdings, dates, err := s.engine.Repo.ListUSInstitutionHoldings(r.Context(), cik, reportDate, assetClass)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cik":          cik,
		"currency":     "USD",
		"report_dates": dates,
		"holdings":     holdings,
	})
}

func (s *WebServer) handleUSStock(w http.ResponseWriter, r *http.Request) {
	sym := strings.TrimSpace(r.PathValue("symbol"))
	items, err := s.engine.Repo.ListUSStockByInstitutions(r.Context(), sym)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"symbol":   strings.ToUpper(sym),
		"currency": "USD",
		"holdings": items,
	})
}

func (s *WebServer) handleHKSouthbound(w http.ResponseWriter, r *http.Request) {
	items, latestDate, err := s.engine.Repo.ListLatestHKSouthbound(r.Context(), 120)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	watches, _ := s.engine.Repo.ListWatchlist(r.Context(), "hk_stock")
	writeJSON(w, http.StatusOK, map[string]any{
		"currency":        "HKD",
		"settlement_date": latestDate,
		"trade_date":      SettlementToTradeDate(latestDate),
		"items":           items,
		"ccass_watchlist": watches,
	})
}

func (s *WebServer) handleHKStockDetail(w http.ResponseWriter, r *http.Request) {
	code := padHKCode(r.PathValue("code"))
	sb, snaps, brokers, err := s.engine.Repo.GetHKStockDetail(r.Context(), code)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code":            code,
		"currency":        "HKD",
		"southbound":      sb,
		"ccass_snapshots": snaps,
		"top_brokers":     brokers,
	})
}

func (s *WebServer) handleListWatchlist(w http.ResponseWriter, r *http.Request) {
	market := strings.TrimSpace(r.URL.Query().Get("market"))
	items, err := s.engine.Repo.ListWatchlist(r.Context(), market)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *WebServer) handleAddWatchlist(w http.ResponseWriter, r *http.Request) {
	var item WatchItem
	if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效请求体"})
		return
	}
	if err := s.engine.Repo.UpsertWatchItem(r.Context(), item); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *WebServer) handleDeleteWatchlist(w http.ResponseWriter, r *http.Request) {
	market := strings.TrimSpace(r.PathValue("market"))
	code := strings.TrimSpace(r.PathValue("code"))
	if err := s.engine.Repo.DeleteWatchItem(r.Context(), market, code); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *WebServer) handleTriggerSync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Market string `json:"market"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Market == "" {
		req.Market = "all"
	}
	s.engine.MarkSyncQueued(req.Market)
	go func(m string) {
		_ = s.engine.TriggerSync(context.Background(), m)
	}(req.Market)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":       true,
		"started":  req.Market,
		"progress": s.engine.SnapshotProgress(),
	})
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, tok string, expUnix int64) {
	isHTTPS := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    tok,
		Path:     "/",
		Expires:  time.Unix(expUnix, 0),
		HttpOnly: true,
		Secure:   isHTTPS,
		SameSite: http.SameSiteLaxMode,
	})
}

func reqIP(r *http.Request) string {
	if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
		return cf
	}
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		if first, _, ok := strings.Cut(xff, ","); ok {
			return strings.TrimSpace(first)
		}
		return xff
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
