package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"intel-hub/internal/store"
)

type authContextKey struct{}

type authPrincipal struct {
	UserID   int64
	Username string
	IsLegacy bool
	RawToken string
}

type ipRateBucket struct {
	fails       int
	lockedUntil time.Time
	windowStart time.Time
}

type authRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*ipRateBucket
}

func newAuthRateLimiter() *authRateLimiter {
	return &authRateLimiter{buckets: make(map[string]*ipRateBucket)}
}

func (l *authRateLimiter) allow(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[ip]
	if !ok {
		return true, 0
	}
	if now.Before(b.lockedUntil) {
		return false, time.Until(b.lockedUntil)
	}
	if now.Sub(b.windowStart) > 15*time.Minute {
		delete(l.buckets, ip)
	}
	return true, 0
}

func (l *authRateLimiter) recordFail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[ip]
	if !ok || now.Sub(b.windowStart) > 15*time.Minute {
		b = &ipRateBucket{windowStart: now}
		l.buckets[ip] = b
	}
	b.fails++
	if b.fails >= 5 {
		b.lockedUntil = now.Add(15 * time.Minute)
	}
}

func (l *authRateLimiter) reset(ip string) {
	l.mu.Lock()
	delete(l.buckets, ip)
	l.mu.Unlock()
}

func clientIP(r *http.Request) string {
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

func (s *Server) authMasterSecret(ctx context.Context) (string, error) {
	return s.store.EnsureAuthMasterSecret(ctx, strings.TrimSpace(s.cfg.Server.APIToken))
}

func principalFromContext(ctx context.Context) (authPrincipal, bool) {
	p, ok := ctx.Value(authContextKey{}).(authPrincipal)
	return p, ok
}

// handleAuthStatus is public so the login UI can show "首次创建账号" vs "账号登录" cleanly.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	n, _ := s.store.CountUsers(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"has_users":        n > 0,
		"requires_setup":   n == 0,
		"setup_key_needed": strings.TrimSpace(s.cfg.Server.APIToken) != "",
	})
}

func (s *Server) handleAuthRegister(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if ok, wait := s.authLimiter.allow(ip); !ok {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "尝试过于频繁，请 " + wait.Round(time.Second).String() + " 后再试",
		})
		return
	}

	var req struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
		SetupKey    string `json:"setup_key"`
		DeviceName  string `json:"device_name"`
		Platform    string `json:"platform"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的请求格式"})
		return
	}

	// Require server setup key (INTEL_API_TOKEN) so strangers on the internet can never register on this private server.
	expectedKey := strings.TrimSpace(s.cfg.Server.APIToken)
	if expectedKey != "" {
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(req.SetupKey)), []byte(expectedKey)) != 1 {
			s.authLimiter.recordFail(ip)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "服务器授权密钥（Setup Key / API Token）校验失败"})
			return
		}
	}

	user, err := s.store.CreateUser(r.Context(), req.Username, req.DisplayName, req.Password)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	tok, exp, err := s.store.CreateSession(r.Context(), user.ID, req.DeviceName, req.Platform)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "创建登录会话失败"})
		return
	}
	s.authLimiter.reset(ip)
	log.Printf("[auth] registered account username=%s platform=%s", user.Username, req.Platform)

	writeJSON(w, http.StatusOK, map[string]any{
		"token":       tok,
		"expires_at":  exp,
		"user":        user,
		"preferences": map[string]any{},
	})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if ok, wait := s.authLimiter.allow(ip); !ok {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "登录失败次数过多，请 " + wait.Round(time.Second).String() + " 后再试",
		})
		return
	}

	var req struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		DeviceName string `json:"device_name"`
		Platform   string `json:"platform"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的请求格式"})
		return
	}

	user, err := s.store.GetUserByUsername(r.Context(), req.Username)
	if err != nil || !store.VerifyPassword(req.Password, user.PasswordHash) {
		s.authLimiter.recordFail(ip)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "用户名或密码错误"})
		return
	}

	tok, exp, err := s.store.CreateSession(r.Context(), user.ID, req.DeviceName, req.Platform)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "创建登录会话失败"})
		return
	}
	s.authLimiter.reset(ip)

	prefs := s.decryptUserPrefsMap(r.Context(), user)
	log.Printf("[auth] login success username=%s platform=%s", user.Username, req.Platform)
	writeJSON(w, http.StatusOK, map[string]any{
		"token":       tok,
		"expires_at":  exp,
		"user":        user,
		"preferences": prefs,
	})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromContext(r.Context())
	if ok && !p.IsLegacy && p.RawToken != "" {
		_ = s.store.RevokeSessionToken(r.Context(), p.RawToken)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromContext(r.Context())
	if !ok || p.IsLegacy {
		writeJSON(w, http.StatusOK, map[string]any{
			"legacy_token_mode": true,
			"user":              nil,
			"preferences":       map[string]any{},
		})
		return
	}
	user, err := s.store.GetUserByID(r.Context(), p.UserID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "账号不存在或会话已失效"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"legacy_token_mode": false,
		"user":              user,
		"preferences":       s.decryptUserPrefsMap(r.Context(), user),
	})
}

func (s *Server) handleUpdatePreferences(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromContext(r.Context())
	if !ok || p.IsLegacy {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "请使用账号登录后再同步云端加密配置"})
		return
	}
	var prefs map[string]any
	if err := json.NewDecoder(r.Body).Decode(&prefs); err != nil || prefs == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json preferences"})
		return
	}
	user, err := s.store.GetUserByID(r.Context(), p.UserID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "账号不存在"})
		return
	}
	master, err := s.authMasterSecret(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "密钥初始化失败"})
		return
	}
	rawJSON, _ := json.Marshal(prefs)
	enc, err := store.EncryptUserPrefs(master, user.EncSalt, string(rawJSON))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "配置加密失败"})
		return
	}
	if err := s.store.UpdateUserPreferencesEnc(r.Context(), user.ID, enc); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存云端加密配置失败"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "encrypted": true, "preferences": prefs})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromContext(r.Context())
	if !ok || p.IsLegacy {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "请先登录个人账号"})
		return
	}
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效请求"})
		return
	}
	user, err := s.store.GetUserByID(r.Context(), p.UserID)
	if err != nil || !store.VerifyPassword(req.OldPassword, user.PasswordHash) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "原密码不正确"})
		return
	}
	if err := s.store.UpdateUserPassword(r.Context(), user.ID, req.NewPassword); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	_ = s.store.RevokeOtherSessions(r.Context(), user.ID, p.RawToken)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) decryptUserPrefsMap(ctx context.Context, user *store.UserAccount) map[string]any {
	out := map[string]any{}
	if user == nil || strings.TrimSpace(user.PreferencesEnc) == "" {
		return out
	}
	master, err := s.authMasterSecret(ctx)
	if err != nil {
		return out
	}
	plain, err := store.DecryptUserPrefs(master, user.EncSalt, user.PreferencesEnc)
	if err != nil {
		return out
	}
	_ = json.Unmarshal([]byte(plain), &out)
	return out
}
