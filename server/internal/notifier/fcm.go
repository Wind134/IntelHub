package notifier

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"intel-hub/internal/model"
)

type serviceAccountKey struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// FCMNotifier sends high-priority Android native wake-up notifications via FCM HTTP v1
// using pure Go stdlib JWT signing (zero heavyweight Google SDK dependencies).
type FCMNotifier struct {
	sa         *serviceAccountKey
	privKey    *rsa.PrivateKey
	projectID  string
	httpClient *http.Client

	mu          sync.Mutex
	accessToken string
	tokenExp    time.Time
}

func NewFCMNotifier(serviceAccountPath, projectIDOverride string) (*FCMNotifier, error) {
	if strings.TrimSpace(serviceAccountPath) == "" {
		return &FCMNotifier{}, nil
	}
	raw, err := os.ReadFile(serviceAccountPath)
	if err != nil {
		return nil, err
	}
	var sa serviceAccountKey
	if err := json.Unmarshal(raw, &sa); err != nil {
		return nil, err
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return nil, fmt.Errorf("invalid private_key pem in fcm service account")
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("fcm service account key is not RSA")
	}
	pid := sa.ProjectID
	if projectIDOverride != "" {
		pid = projectIDOverride
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return &FCMNotifier{
		sa:         &sa,
		privKey:    rsaKey,
		projectID:  pid,
		httpClient: &http.Client{Timeout: 12 * time.Second},
	}, nil
}

func (f *FCMNotifier) Enabled() bool {
	return f != nil && f.sa != nil && f.privKey != nil && f.projectID != ""
}

// SendToDevice dispatches a high-priority Android notification mapped to the right Notification Channel.
func (f *FCMNotifier) SendToDevice(ctx context.Context, device model.ClientDevice, ev *model.IntelEvent) error {
	if !f.Enabled() || device.FCMToken == "" {
		return nil
	}
	tok, err := f.getAccessToken(ctx)
	if err != nil {
		return err
	}

	channelID := "intel_high"
	titlePrefix := "🟠"
	if ev.Score >= 9 {
		channelID = "intel_critical"
		titlePrefix = "🔴"
	}

	payload := map[string]any{
		"message": map[string]any{
			"token": device.FCMToken,
			"notification": map[string]any{
				"title": fmt.Sprintf("%s [%d/10 %s] %s", titlePrefix, ev.Score, categoryLabel(ev.Category), ev.ZhTitle),
				"body":  ev.ImpactAnalysis,
			},
			"data": map[string]string{
				"event_code": ev.EventCode,
				"event_id":   fmt.Sprintf("%d", ev.ID),
				"category":   ev.Category,
				"score":      fmt.Sprintf("%d", ev.Score),
				"url":        ev.CanonicalURL,
			},
			"android": map[string]any{
				"priority": "HIGH",
				"notification": map[string]any{
					"channel_id":            channelID,
					"notification_priority": "PRIORITY_MAX",
					"default_sound":         true,
					"default_vibrate_timings": true,
				},
			},
		},
	}

	body, _ := json.Marshal(payload)
	endpoint := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", f.projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("fcm http %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

func (f *FCMNotifier) getAccessToken(ctx context.Context) (string, error) {
	f.mu.Lock()
	if f.accessToken != "" && time.Now().Before(f.tokenExp) {
		tok := f.accessToken
		f.mu.Unlock()
		return tok, nil
	}
	f.mu.Unlock()

	now := time.Now().Unix()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claimsJSON, _ := json.Marshal(map[string]any{
		"iss":   f.sa.ClientEmail,
		"scope": "https://www.googleapis.com/auth/firebase.messaging",
		"aud":   f.sa.TokenURI,
		"iat":   now,
		"exp":   now + 3600,
	})
	claims := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := header + "." + claims
	hashed := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.privKey, crypto.SHA256, hashed[:])
	if err != nil {
		return "", err
	}
	assertion := signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)

	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.sa.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var tokResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokResp); err != nil {
		return "", err
	}
	if tokResp.AccessToken == "" {
		return "", fmt.Errorf("empty oauth access token from google")
	}

	f.mu.Lock()
	f.accessToken = tokResp.AccessToken
	f.tokenExp = time.Now().Add(time.Duration(tokResp.ExpiresIn-120) * time.Second)
	f.mu.Unlock()
	return tokResp.AccessToken, nil
}
