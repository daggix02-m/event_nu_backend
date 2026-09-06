package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestRequestIDGeneratesAndEchoes(t *testing.T) {
	var seen string
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = r.Context().Value(RequestIDKey).(string)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	handler.ServeHTTP(rec, req)

	if seen == "" {
		t.Fatal("expected a generated request ID in context")
	}
	if got := rec.Header().Get("X-Request-ID"); got != seen {
		t.Fatalf("expected echoed header %q, got %q", seen, got)
	}
}

func TestRequestIDPreservesIncoming(t *testing.T) {
	var seen string
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = r.Context().Value(RequestIDKey).(string)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "client-supplied")

	handler.ServeHTTP(rec, req)

	if seen != "client-supplied" {
		t.Fatalf("expected client-supplied ID, got %q", seen)
	}
}

func TestSecureHeadersSet(t *testing.T) {
	handler := SecureHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	for _, h := range []string{"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Content-Security-Policy"} {
		if rec.Header().Get(h) == "" {
			t.Fatalf("expected security header %q to be set", h)
		}
	}
}

func TestRecoverPanicConvertsTo500(t *testing.T) {
	handler := RecoverPanic(testLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestCORSPreflight(t *testing.T) {
	handler := CORS(map[string]struct{}{"https://admin.example.com": {}})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://admin.example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 preflight, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://admin.example.com" {
		t.Fatalf("expected allow-origin header, got %q", got)
	}
}

// TestLogRequestRedactsQueryString proves LogRequest never emits the raw query
// string, so secrets passed as query parameters (codes, tokens) cannot leak
// into log output.
func TestLogRequestRedactsQueryString(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	handler := LogRequest(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/verify?code=SECRET&access_token=X", nil)

	handler.ServeHTTP(rec, req)

	out := buf.String()
	for _, secret := range []string{"SECRET", "access_token=X", "code=SECRET"} {
		if strings.Contains(out, secret) {
			t.Fatalf("query-string secret %q leaked into logs: %s", secret, out)
		}
	}
	// The path itself is still logged (query stripped).
	if !strings.Contains(out, "path=/api/v1/auth/verify") {
		t.Fatalf("expected path to be logged, got: %s", out)
	}
}
