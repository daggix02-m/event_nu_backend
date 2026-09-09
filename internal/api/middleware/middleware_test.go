package middleware

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/service"
	"github.com/golang-jwt/jwt/v5"
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

// TestCORSDisallowedOriginNotEchoed proves a request from an origin not in the
// allow-list gets no CORS headers and passes through to the handler.
func TestCORSDisallowedOriginNotEchoed(t *testing.T) {
	reached := false
	handler := CORS(map[string]struct{}{"https://admin.example.com": {}})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reached = true
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !reached {
		t.Fatal("a disallowed-origin GET must still reach the handler")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("disallowed origin must not be echoed, got %q", got)
	}
}

// TestCORSDisallowedOriginPreflightBlocked proves a disallowed-origin OPTIONS
// preflight returns 204 but with no allow-origin header (no CORS grant).
func TestCORSDisallowedOriginPreflightBlocked(t *testing.T) {
	handler := CORS(map[string]struct{}{"https://admin.example.com": {}})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("a preflight must not reach the handler")
		}),
	)

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 preflight, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("disallowed origin must not be echoed on preflight, got %q", got)
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

// TestRequestTimeoutCancelsSlowHandler proves the deadline is propagated to the
// handler's request context, so downstream work observes the cancellation and
// stops (the handler integration tests always finish well inside the timeout
// and never exercise this branch).
func TestRequestTimeoutCancelsSlowHandler(t *testing.T) {
	cancelled := make(chan struct{})
	handler := RequestTimeout(20 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(cancelled)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/slow", nil)

	handler.ServeHTTP(rec, req)

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("expected the handler to observe the request context cancellation")
	}
}

// TestLogRequestRecordsStatus proves the wrapper captures a non-200 status
// written by the downstream handler.
func TestLogRequestRecordsStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	handler := LogRequest(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !strings.Contains(buf.String(), "status=418") {
		t.Fatalf("expected status=418 in log, got: %s", buf.String())
	}
}

// newAuthServiceForTest builds an AuthService whose stateless ParseAccessToken
// works against a fixed secret; no stores are needed (never reached).
func newAuthServiceForTest() *service.AuthService {
	return service.NewAuthService(nil, nil, config.Config{JWTSecret: "unit-test-secret"})
}

// signTestToken mints a valid HS256 access token with the same secret and
// claims (sub/role/exp) the auth service issues.
func signTestToken(t *testing.T, userID, role string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  userID,
		"role": role,
		"exp":  time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte("unit-test-secret"))
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}
	return signed
}

// TestRequireAuthValidTokenInjectsUserID proves a valid Bearer token passes
// through and its subject lands in the request context under UserIDKey.
func TestRequireAuthValidTokenInjectsUserID(t *testing.T) {
	var got string
	handler := RequireAuth(newAuthServiceForTest())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = UserID(r.Context())
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+signTestToken(t, "user-1", "member"))

	handler.ServeHTTP(rec, req)

	if got != "user-1" {
		t.Fatalf("expected user-1 in context, got %q", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 pass-through, got %d", rec.Code)
	}
}

// TestRequireAuthMissingBearerRejected proves requests without an
// Authorization header get a 401 and never reach the handler.
func TestRequireAuthMissingBearerRejected(t *testing.T) {
	reached := false
	handler := RequireAuth(newAuthServiceForTest())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if reached {
		t.Fatal("downstream handler must not run without a bearer token")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// TestRequireAuthNonBearerRejected proves a non-Bearer Authorization header is
// rejected the same way.
func TestRequireAuthNonBearerRejected(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic abc123")

	RequireAuth(newAuthServiceForTest())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("downstream handler must not run")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// TestRequireAuthInvalidTokenRejected proves an unparseable/invalid Bearer
// token is rejected.
func TestRequireAuthInvalidTokenRejected(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer not.a.token")

	RequireAuth(newAuthServiceForTest())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("downstream handler must not run")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// TestRequireAuthRejectsTokenSignedWithWrongKey proves a JWT signed with a
// different secret fails signature validation.
func TestRequireAuthRejectsTokenSignedWithWrongKey(t *testing.T) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte("other-secret"))
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+signed)

	RequireAuth(newAuthServiceForTest())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("downstream handler must not run")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestUserIDWithoutContextIsEmpty(t *testing.T) {
	if got := UserID(context.Background()); got != "" {
		t.Fatalf("expected empty user id from a bare context, got %q", got)
	}
}

// TestRequireAdminMissingTokenRejected proves RequireAdmin rejects unauthenticated
// requests before any DB work.
func TestRequireAdminMissingTokenRejected(t *testing.T) {
	rec := httptest.NewRecorder()
	RequireAdmin(newAuthServiceForTest())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("downstream handler must not run")
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// TestRequireAdminMissingTxReturns500 proves a valid token with no request
// transaction in context (BeginRequestTx not applied) is a server error, not a
// silent pass-through.
func TestRequireAdminMissingTxReturns500(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.Header.Set("Authorization", "Bearer "+signTestToken(t, "admin-1", "admin"))

	RequireAdmin(newAuthServiceForTest())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("downstream handler must not run without a request tx")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when the request tx is missing, got %d", rec.Code)
	}
}
