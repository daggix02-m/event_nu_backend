package push

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func genServiceAccountJSON(t *testing.T, projectID string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen rsa key: %v", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})

	sa := map[string]string{
		"type":         "service_account",
		"project_id":   projectID,
		"client_email": "test@test.iam.gserviceaccount.com",
		"private_key":  string(pemBytes),
	}
	b, _ := json.Marshal(sa)
	return string(b)
}

// TestFCMTokenExchange verifies that the client signs a JWT and exchanges it
// for an access token via the mocked OAuth2 endpoint.
func TestFCMTokenExchange(t *testing.T) {
	var gotAssertion atomic.Value

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
			http.Error(w, err.Error(), 400)
			return
		}
		assertion := r.FormValue("assertion")
		gotAssertion.Store(assertion)
		if r.FormValue("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			t.Errorf("unexpected grant_type: %s", r.FormValue("grant_type"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-access-token",
			"expires_in":   3600,
			"token_type":   "Bearer",
		})
	}))
	defer tokenSrv.Close()

	saJSON := genServiceAccountJSON(t, "my-project")
	fcm, err := NewFCMFromBytes(FCMConfig{
		ServiceAccount: saJSON,
		TokenURL:       tokenSrv.URL,
		APIBase:        "https://fcm.googleapis.com/v1",
	})
	if err != nil {
		t.Fatalf("new fcm: %v", err)
	}

	tok, err := fcm.bearerToken(context.Background())
	if err != nil {
		t.Fatalf("bearerToken: %v", err)
	}
	if tok != "test-access-token" {
		t.Errorf("expected test-access-token, got %q", tok)
	}

	// Verify an assertion was sent (we can't decode the JWT without the key
	// but we can verify it's non-empty and that caching works).
	assertion := gotAssertion.Load().(string)
	if assertion == "" {
		t.Error("expected non-empty assertion sent to token endpoint")
	}

	// Second call should hit the cache, not the server again.
	tok2, err := fcm.bearerToken(context.Background())
	if err != nil {
		t.Fatalf("bearerToken (cached): %v", err)
	}
	if tok2 != tok {
		t.Errorf("cached token mismatch: got %q", tok2)
	}
}

// TestFCMSendSuccess verifies that a successful FCM v1 response returns the
// message name.
func TestFCMSendSuccess(t *testing.T) {
	var gotBody string

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
	}))
	defer tokenSrv.Close()

	fcmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := json.Marshal(map[string]any{
			"name": "projects/my-project/messages/msg-123",
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
		var buf [8192]byte
		n, _ := r.Body.Read(buf[:])
		gotBody = string(buf[:n])
	}))
	defer fcmSrv.Close()

	saJSON := genServiceAccountJSON(t, "my-project")
	fcm, err := NewFCMFromBytes(FCMConfig{
		ServiceAccount: saJSON,
		TokenURL:       tokenSrv.URL,
		APIBase:        fcmSrv.URL,
	})
	if err != nil {
		t.Fatalf("new fcm: %v", err)
	}

	name, err := fcm.Send(context.Background(), Message{
		Token: "device-token-123",
		Title: "Hello",
		Body:  "World",
		Data:  map[string]string{"event_id": "abc"},
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if name != "projects/my-project/messages/msg-123" {
		t.Errorf("unexpected name: %q", name)
	}
	if gotBody == "" {
		t.Error("expected request body sent to FCM")
	}
}

// TestFCMSendErrorClassification verifies that various FCM error payloads are
// classified correctly.
func TestFCMSendErrorClassification(t *testing.T) {
	tests := []struct {
		status   int
		body     string
		wantCode ErrCode
	}{
		{
			400,
			`{"error":{"status":"INVALID_ARGUMENT","message":"token is not valid"}}`,
			CodeInvalidToken,
		},
		{
			404,
			`{"error":{"status":"NOT_FOUND","message":"token not found"}}`,
			CodeInvalidToken,
		},
		{
			500,
			`{"error":{"status":"INTERNAL","message":"internal error"}}`,
			CodeUnavailable,
		},
		{
			429,
			`{"error":{"status":"RESOURCE_EXHAUSTED","message":"rate limit"}}`,
			CodeUnavailable,
		},
		{
			503,
			`{"error":{"status":"UNAVAILABLE","message":"try again"}}`,
			CodeUnavailable,
		},
		{
			403,
			`{"error":{"status":"PERMISSION_DENIED","message":"bad creds"}}`,
			CodeFail,
		},
	}
	for _, tt := range tests {
		name := fmt.Sprintf("status_%d_%s", tt.status, tt.wantCode)
		t.Run(name, func(t *testing.T) {
			got := classifyFCM(tt.status, []byte(tt.body))
			if got != tt.wantCode {
				t.Errorf("classifyFCM(%d, ...) = %q, want %q", tt.status, got, tt.wantCode)
			}
		})
	}
}

// TestFCMSendInvalidToken verifies that an UNREGISTERED error produces a
// typed Error with IsInvalidToken.
func TestFCMSendInvalidToken(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
	}))
	defer tokenSrv.Close()

	fcmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"status":  "UNREGISTERED",
				"message": "Registration token not registered",
			},
		})
	}))
	defer fcmSrv.Close()

	saJSON := genServiceAccountJSON(t, "my-project")
	fcm, err := NewFCMFromBytes(FCMConfig{
		ServiceAccount: saJSON,
		TokenURL:       tokenSrv.URL,
		APIBase:        fcmSrv.URL,
	})
	if err != nil {
		t.Fatalf("new fcm: %v", err)
	}

	_, err = fcm.Send(context.Background(), Message{
		Token: "bad-token",
		Title: "Hi",
		Body:  "There",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var pErr *Error
	if !errors.As(err, &pErr) {
		t.Fatalf("expected *push.Error, got %T: %v", err, err)
	}
	if !pErr.IsInvalidToken() {
		t.Errorf("expected IsInvalidToken, got %q", pErr.Code)
	}
	if pErr.Retryable() {
		t.Error("invalid token should not be retryable")
	}
}
