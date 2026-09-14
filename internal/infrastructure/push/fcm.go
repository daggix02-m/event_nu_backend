package push

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
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

	"github.com/golang-jwt/jwt/v5"
)

const (
	fcmDefaultAPIBase  = "https://fcm.googleapis.com/v1"
	fcmDefaultTokenURL = "https://oauth2.googleapis.com/token"
)

// FCMConfig is the subset of config.Config the FCM client needs. Passing them
// as a struct avoids a circular import from infrastructure → config.
type FCMConfig struct {
	ProjectID      string
	ServiceAccount string // JSON blob; takes precedence over file path
	APIBase        string
	TokenURL       string
}

// serviceAccountJSON models the minimum fields the Firebase service-account
// JSON must expose for token generation.
type serviceAccountJSON struct {
	ProjectID    string `json:"project_id"`
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	PrivateKeyID string `json:"private_key_id"`
}

// FCMClient sends push notifications via the Firebase Cloud Messaging v1 API.
type FCMClient struct {
	projectID string
	tokenURL  string
	apiBase   string
	http      *http.Client

	clientEmail  string
	privateKeyID string
	privateKey   *rsa.PrivateKey

	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time
}

// NewFCMFromBytes parses the raw service-account JSON bytes and returns a
// ready-to-send client. An empty ProjectID in cfg is filled from the JSON.
func NewFCMFromBytes(cfg FCMConfig) (*FCMClient, error) {
	return newFCMFromRaw(cfg)
}

// NewFCMFromEnvFile reads the service-account file at path, applies defaults
// suitable for tests (FCM_API_BASE / FCM_TOKEN_URL overrides), and returns
// a ready-to-send client.
func NewFCMFromEnvFile(path, apiBase, tokenURL string) (*FCMClient, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("push fcm: read service account file: %w", err)
	}
	cfg := FCMConfig{
		ServiceAccount: string(b),
		APIBase:        apiBase,
		TokenURL:       tokenURL,
	}
	return newFCMFromRaw(cfg)
}

func newFCMFromRaw(cfg FCMConfig) (*FCMClient, error) {
	var sa serviceAccountJSON
	if err := json.Unmarshal([]byte(cfg.ServiceAccount), &sa); err != nil {
		return nil, fmt.Errorf("push fcm: invalid service account JSON: %w", err)
	}
	projectID := cfg.ProjectID
	if projectID == "" {
		projectID = sa.ProjectID
	}
	if projectID == "" {
		return nil, fmt.Errorf("push fcm: project_id missing (set FCM_PROJECT_ID or include it in the service account JSON)")
	}
	if sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, fmt.Errorf("push fcm: service account missing client_email or private_key")
	}

	key, err := parsePrivateKey(sa.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("push fcm: %w", err)
	}

	tokenURL := cfg.TokenURL
	if tokenURL == "" {
		tokenURL = fcmDefaultTokenURL
	}
	apiBase := cfg.APIBase
	if apiBase == "" {
		apiBase = fcmDefaultAPIBase
	}

	return &FCMClient{
		projectID:    projectID,
		tokenURL:     tokenURL,
		apiBase:      apiBase,
		http:         &http.Client{Timeout: 30 * time.Second},
		clientEmail:  sa.ClientEmail,
		privateKeyID: sa.PrivateKeyID,
		privateKey:   key,
	}, nil
}

// bearerToken returns a valid OAuth2 access token, refreshing it when near
// expiry. Tokens are cached for 45 minutes (FCM tokens are valid for 1 hour).
func (c *FCMClient) bearerToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if c.accessToken != "" && now.Before(c.tokenExpiry) {
		return c.accessToken, nil
	}

	claims := jwt.MapClaims{
		"iss":   c.clientEmail,
		"sub":   c.clientEmail,
		"aud":   c.tokenURL,
		"iat":   now.Unix(),
		"exp":   now.Add(45 * time.Minute).Unix(),
		"scope": "https://www.googleapis.com/auth/firebase.messaging",
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = c.privateKeyID

	signed, err := tok.SignedString(c.privateKey)
	if err != nil {
		return "", fmt.Errorf("push fcm: sign token: %w", err)
	}

	body := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {signed},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(body.Encode()))
	if err != nil {
		return "", fmt.Errorf("push fcm: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("push fcm: token endpoint: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("push fcm: token endpoint status %d: %s", resp.StatusCode, string(respBody))
	}

	var tokResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(respBody, &tokResp); err != nil {
		return "", fmt.Errorf("push fcm: decode token response: %w", err)
	}
	if tokResp.AccessToken == "" {
		return "", fmt.Errorf("push fcm: token endpoint returned empty access_token")
	}

	c.accessToken = tokResp.AccessToken
	c.tokenExpiry = now.Add(time.Duration(tokResp.ExpiresIn)*time.Second - 30*time.Second)
	return c.accessToken, nil
}

func (c *FCMClient) Name() string { return "fcm" }

// Send delivers a single push notification via FCM v1. The message name is
// returned on success.
func (c *FCMClient) Send(ctx context.Context, m Message) (string, error) {
	bearer, err := c.bearerToken(ctx)
	if err != nil {
		return "", err
	}

	msg := map[string]any{
		"message": map[string]any{
			"token": m.Token,
			"notification": map[string]string{
				"title": m.Title,
				"body":  m.Body,
			},
		},
	}
	if len(m.Data) > 0 {
		msg["message"].(map[string]any)["data"] = m.Data
	}

	raw, _ := json.Marshal(msg)
	apiURL := fmt.Sprintf("%s/projects/%s/messages:send", c.apiBase, url.PathEscape(c.projectID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("push fcm: build send request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", &Error{Code: CodeUnavailable, Msg: fmt.Sprintf("network: %v", err)}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == http.StatusOK {
		var r struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(respBody, &r)
		return r.Name, nil
	}

	code := classifyFCM(resp.StatusCode, respBody)
	return "", &Error{
		Code: code,
		Msg:  fmt.Sprintf("fcm v1 status %d: %s", resp.StatusCode, string(respBody)),
	}
}

// classifyFCM maps HTTP status + JSON error.status to an ErrCode.
func classifyFCM(status int, body []byte) ErrCode {
	raw := string(body)

	type fcmError struct {
		Error struct {
			Status string `json:"status"`
		} `json:"error"`
	}
	var fcmErr fcmError
	_ = json.Unmarshal(body, &fcmErr)
	errStatus := fcmErr.Error.Status

	// Invalid / unregistered token → prune device.
	for _, bad := range []string{
		"UNREGISTERED", "INVALID_ARGUMENT", "SENDER_ID_MISMATCH", "NOT_FOUND",
	} {
		if errStatus == bad || strings.Contains(raw, bad) {
			return CodeInvalidToken
		}
	}

	// Retryable / transient → backoff.
	for _, retry := range []string{
		"UNAVAILABLE", "INTERNAL", "DEADLINE_EXCEEDED", "RESOURCE_EXHAUSTED",
	} {
		if errStatus == retry || strings.Contains(raw, retry) {
			return CodeUnavailable
		}
	}
	if status == http.StatusTooManyRequests || status >= http.StatusInternalServerError {
		return CodeUnavailable
	}

	return CodeFail
}

// parsePrivateKey extracts an RSA key from the PEM string in the service
// account JSON (PKCS8 primary; PKCS1 fallback).
func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in private_key")
	}

	// PKCS8 (type "PRIVATE KEY") — standard for Firebase service accounts.
	if block.Type == "PRIVATE KEY" {
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS8 private key: %w", err)
		}
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not RSA (got %T)", key)
		}
		return rsaKey, nil
	}

	// PKCS1 (type "RSA PRIVATE KEY") — legacy fallback.
	if block.Type == "RSA PRIVATE KEY" {
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS1 private key: %w", err)
		}
		return key, nil
	}

	return nil, fmt.Errorf("unsupported PEM block type %q", block.Type)
}
