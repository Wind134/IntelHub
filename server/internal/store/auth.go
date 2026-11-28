package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	pbkdf2Iterations = 210000
	sessionTTL       = 30 * 24 * time.Hour
)

type UserAccount struct {
	ID             int64  `json:"id"`
	Username       string `json:"username"`
	DisplayName    string `json:"display_name"`
	PasswordHash   string `json:"-"`
	EncSalt        string `json:"-"`
	PreferencesEnc string `json:"-"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

type UserSession struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"user_id"`
	Username   string `json:"username"`
	DeviceName string `json:"device_name"`
	Platform   string `json:"platform"`
	ExpiresAt  int64  `json:"expires_at"`
	LastUsedAt int64  `json:"last_used_at"`
	CreatedAt  int64  `json:"created_at"`
}

func (s *Store) migrateAuth() error {
	const authSchema = `
CREATE TABLE IF NOT EXISTS users (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    username        TEXT NOT NULL UNIQUE,
    display_name    TEXT NOT NULL DEFAULT '',
    password_hash   TEXT NOT NULL,
    enc_salt        TEXT NOT NULL,
    preferences_enc TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS user_sessions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,
    device_name  TEXT NOT NULL DEFAULT '',
    platform     TEXT NOT NULL DEFAULT '',
    expires_at   INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL,
    created_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON user_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_exp ON user_sessions(expires_at);
`
	_, err := s.writeDB.Exec(authSchema)
	return err
}

// HashPassword hashes a plaintext password with PBKDF2-HMAC-SHA256 (210,000 iterations + 16-byte random salt).
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s",
		pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(dk),
	), nil
}

// VerifyPassword performs a constant-time PBKDF2-HMAC-SHA256 verification.
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	var iters int
	if _, err := fmt.Sscanf(parts[1], "%d", &iters); err != nil || iters < 10000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) < 8 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(expected) == 0 {
		return false
	}
	actual, err := pbkdf2.Key(sha256.New, password, salt, iters, len(expected))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

// EncryptUserPrefs encrypts a JSON preferences string using AES-256-GCM with an HKDF-derived per-user key.
func EncryptUserPrefs(masterSecret, userSaltB64, plaintextJSON string) (string, error) {
	if strings.TrimSpace(plaintextJSON) == "" {
		return "", nil
	}
	key, err := deriveUserAESKey(masterSecret, userSaltB64)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintextJSON), []byte("informationHub-prefs-v1"))
	return "v1:" + base64.RawStdEncoding.EncodeToString(nonce) + ":" + base64.RawStdEncoding.EncodeToString(ciphertext), nil
}

// DecryptUserPrefs decrypts an AES-256-GCM preferences blob back into JSON.
func DecryptUserPrefs(masterSecret, userSaltB64, encoded string) (string, error) {
	if strings.TrimSpace(encoded) == "" {
		return "{}", nil
	}
	parts := strings.Split(encoded, ":")
	if len(parts) != 3 || parts[0] != "v1" {
		return "", fmt.Errorf("invalid encrypted prefs format")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return "", err
	}
	key, err := deriveUserAESKey(masterSecret, userSaltB64)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, []byte("informationHub-prefs-v1"))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func deriveUserAESKey(masterSecret, userSaltB64 string) ([]byte, error) {
	salt, err := base64.RawStdEncoding.DecodeString(userSaltB64)
	if err != nil || len(salt) == 0 {
		salt = []byte(userSaltB64)
	}
	return hkdf.Key(sha256.New, []byte(masterSecret), salt, "informationHub-user-prefs-v1", 32)
}

func hashSessionToken(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

func generateRandomSaltB64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(b), nil
}

// EnsureAuthMasterSecret returns a persistent 32-byte server encryption key (derived from configured secret or generated once).
func (s *Store) EnsureAuthMasterSecret(ctx context.Context, configured string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		return configured, nil
	}
	if saved, err := s.GetSetting(ctx, "auth_master_secret"); err == nil && saved != "" {
		return saved, nil
	}
	gen, err := generateRandomSaltB64(32)
	if err != nil {
		return "", err
	}
	if err := s.SetSetting(ctx, "auth_master_secret", gen); err != nil {
		return "", err
	}
	return gen, nil
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(ctx context.Context, username, displayName, password string) (*UserAccount, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if len(username) < 2 || len(username) > 40 {
		return nil, fmt.Errorf("用户名长度需在 2~40 个字符之间")
	}
	if len(password) < 8 {
		return nil, fmt.Errorf("密码长度不能少于 8 位")
	}
	if strings.TrimSpace(displayName) == "" {
		displayName = username
	}
	pwHash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	encSalt, err := generateRandomSaltB64(16)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()

	s.writeMu.Lock()
	res, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO users (username, display_name, password_hash, enc_salt, preferences_enc, created_at, updated_at)
		VALUES (?, ?, ?, ?, '', ?, ?)
	`, username, strings.TrimSpace(displayName), pwHash, encSalt, now, now)
	s.writeMu.Unlock()
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, fmt.Errorf("用户名已存在")
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &UserAccount{
		ID:          id,
		Username:    username,
		DisplayName: strings.TrimSpace(displayName),
		EncSalt:     encSalt,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*UserAccount, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	var u UserAccount
	err := s.readDB.QueryRowContext(ctx, `
		SELECT id, username, display_name, password_hash, enc_salt, preferences_enc, created_at, updated_at
		FROM users WHERE username = ?
	`, username).Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.EncSalt, &u.PreferencesEnc, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) GetUserByID(ctx context.Context, id int64) (*UserAccount, error) {
	var u UserAccount
	err := s.readDB.QueryRowContext(ctx, `
		SELECT id, username, display_name, password_hash, enc_salt, preferences_enc, created_at, updated_at
		FROM users WHERE id = ?
	`, id).Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.EncSalt, &u.PreferencesEnc, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) UpdateUserPreferencesEnc(ctx context.Context, userID int64, prefsEnc string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.writeDB.ExecContext(ctx, `
		UPDATE users SET preferences_enc = ?, updated_at = ? WHERE id = ?
	`, prefsEnc, time.Now().Unix(), userID)
	return err
}

func (s *Store) UpdateUserPassword(ctx context.Context, userID int64, newPassword string) error {
	if len(newPassword) < 8 {
		return fmt.Errorf("新密码长度不能少于 8 位")
	}
	pwHash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.writeDB.ExecContext(ctx, `
		UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?
	`, pwHash, time.Now().Unix(), userID)
	return err
}

// CreateSession issues a cryptographically random 256-bit bearer token and persists only its SHA-256 hash.
func (s *Store) CreateSession(ctx context.Context, userID int64, deviceName, platform string) (string, int64, error) {
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		return "", 0, err
	}
	rawToken := "ihs_" + base64.RawURLEncoding.EncodeToString(rawBytes)
	tokHash := hashSessionToken(rawToken)
	now := time.Now().Unix()
	exp := time.Now().Add(sessionTTL).Unix()

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = s.writeDB.ExecContext(ctx, `DELETE FROM user_sessions WHERE expires_at < ?`, now)
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO user_sessions (user_id, token_hash, device_name, platform, expires_at, last_used_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, userID, tokHash, strings.TrimSpace(deviceName), strings.TrimSpace(platform), exp, now, now)
	if err != nil {
		return "", 0, err
	}
	return rawToken, exp, nil
}

// ValidateSessionToken verifies a bearer token and returns the associated user session if active.
func (s *Store) ValidateSessionToken(ctx context.Context, rawToken string) (*UserSession, error) {
	if !strings.HasPrefix(rawToken, "ihs_") {
		return nil, sql.ErrNoRows
	}
	tokHash := hashSessionToken(rawToken)
	now := time.Now().Unix()
	var sess UserSession
	err := s.readDB.QueryRowContext(ctx, `
		SELECT s.id, s.user_id, u.username, s.device_name, s.platform, s.expires_at, s.last_used_at, s.created_at
		FROM user_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?
	`, tokHash, now).Scan(&sess.ID, &sess.UserID, &sess.Username, &sess.DeviceName, &sess.Platform, &sess.ExpiresAt, &sess.LastUsedAt, &sess.CreatedAt)
	if err != nil {
		return nil, err
	}
	// Rolling session renewal if more than 24h since last refresh
	if now-sess.LastUsedAt > 86400 {
		newExp := time.Now().Add(sessionTTL).Unix()
		go func(id int64, ts, exp int64) {
			s.writeMu.Lock()
			_, _ = s.writeDB.Exec(`UPDATE user_sessions SET last_used_at = ?, expires_at = ? WHERE id = ?`, ts, exp, id)
			s.writeMu.Unlock()
		}(sess.ID, now, newExp)
	}
	return &sess, nil
}

func (s *Store) RevokeSessionToken(ctx context.Context, rawToken string) error {
	if rawToken == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.writeDB.ExecContext(ctx, `DELETE FROM user_sessions WHERE token_hash = ?`, hashSessionToken(rawToken))
	return err
}

func (s *Store) RevokeOtherSessions(ctx context.Context, userID int64, keepRawToken string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if keepRawToken != "" {
		_, err := s.writeDB.ExecContext(ctx, `DELETE FROM user_sessions WHERE user_id = ? AND token_hash != ?`, userID, hashSessionToken(keepRawToken))
		return err
	}
	_, err := s.writeDB.ExecContext(ctx, `DELETE FROM user_sessions WHERE user_id = ?`, userID)
	return err
}

// ErrAuthUnauthorized marks failed credential checks.
var ErrAuthUnauthorized = errors.New("unauthorized")
