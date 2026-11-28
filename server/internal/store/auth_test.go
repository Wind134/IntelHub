package store

import (
	"context"
	"testing"
)

func TestPasswordAndEncryptedPrefsAndSessions(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open memory store: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	// 1. User creation + PBKDF2 password verification
	u, err := st.CreateUser(ctx, "Alice", "Alice Chen", "Str0ngPass!2026")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	loaded, err := st.GetUserByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if !VerifyPassword("Str0ngPass!2026", loaded.PasswordHash) {
		t.Fatalf("expected valid password to verify")
	}
	if VerifyPassword("WrongPassword!", loaded.PasswordHash) {
		t.Fatalf("expected invalid password to fail")
	}

	// 2. AES-256-GCM encrypted user preferences bound to account
	master, err := st.EnsureAuthMasterSecret(ctx, "test-master-secret-key")
	if err != nil {
		t.Fatalf("master secret: %v", err)
	}
	origJSON := `{"theme_mode":"dark","min_push_score":8,"high_fps_motion":true,"quiet_hours_range":"00:30-07:30"}`
	enc, err := EncryptUserPrefs(master, loaded.EncSalt, origJSON)
	if err != nil {
		t.Fatalf("encrypt prefs: %v", err)
	}
	if enc == origJSON || len(enc) < 20 {
		t.Fatalf("expected encrypted ciphertext, got %q", enc)
	}
	if err := st.UpdateUserPreferencesEnc(ctx, u.ID, enc); err != nil {
		t.Fatalf("save encrypted prefs: %v", err)
	}
	reloaded, _ := st.GetUserByID(ctx, u.ID)
	dec, err := DecryptUserPrefs(master, reloaded.EncSalt, reloaded.PreferencesEnc)
	if err != nil || dec != origJSON {
		t.Fatalf("decrypt mismatch: got %q err=%v", dec, err)
	}

	// 3. Session creation, validation, and revocation
	tok, _, err := st.CreateSession(ctx, u.ID, "Windows 11", "windows")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	sess, err := st.ValidateSessionToken(ctx, tok)
	if err != nil || sess.Username != "alice" {
		t.Fatalf("validate session failed: %v", err)
	}
	if err := st.RevokeSessionToken(ctx, tok); err != nil {
		t.Fatalf("revoke session: %v", err)
	}
	if _, err := st.ValidateSessionToken(ctx, tok); err == nil {
		t.Fatalf("expected revoked token to fail validation")
	}
}
