package routes_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/routes"
	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/joho/godotenv"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func loadEnvForTest(t *testing.T) {
	t.Helper()
	if os.Getenv("DATABASE_URL") != "" {
		return
	}
	dir, _ := os.Getwd()
	for {
		p := filepath.Join(dir, ".env")
		if _, err := os.Stat(p); err == nil {
			_ = godotenv.Load(p)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	loadEnvForTest(t)

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping DB-backed routes tests")
	}

	pool, err := database.NewPool(context.Background(), dsn)
	if err != nil {
		t.Skipf("database unreachable (%v) — skipping DB-backed routes tests", err)
	}
	t.Cleanup(pool.Close)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	app := api.NewApp(testLogger(), cfg, pool)
	t.Cleanup(app.RateLimiter.Stop)
	return routes.New(app)
}

func doReq(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthzRoute(t *testing.T) {
	h := newTestHandler(t)
	rec := doReq(t, h, http.MethodGet, "/healthz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestReadyzRoute(t *testing.T) {
	h := newTestHandler(t)
	rec := doReq(t, h, http.MethodGet, "/readyz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (database reachable), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMetricsRouteIsPublicNoAuth(t *testing.T) {
	h := newTestHandler(t)
	rec := doReq(t, h, http.MethodGet, "/metrics", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"http_requests_total", "process_goroutines", "process_alloc_bytes", "process_gc_count"} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
}

// TestEventMalformedUUIDIs404Not500 proves the end-to-end guarantee of
// phase-09: a malformed {id} yields a clean JSON 404 before any DB access,
// never the Postgres 22P02 500 it would have produced previously.
func TestEventMalformedUUIDIs404Not500(t *testing.T) {
	h := newTestHandler(t)
	rec := doReq(t, h, http.MethodGet, "/api/v1/events/not-a-uuid", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (not 500), got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected a JSON app-level 404, got Content-Type %q", ct)
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	h := newTestHandler(t)
	for _, path := range []string{"/nope", "/api/v1/nope"} {
		if rec := doReq(t, h, http.MethodGet, path, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: expected 404, got %d", path, rec.Code)
		}
	}
}

func TestMethodMismatchIs405(t *testing.T) {
	h := newTestHandler(t)
	rec := doReq(t, h, http.MethodPost, "/healthz", "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestProtectedRouteRequiresAuth(t *testing.T) {
	h := newTestHandler(t)
	rec := doReq(t, h, http.MethodGet, "/api/v1/users/me", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without a token, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminRouteRequiresAuth(t *testing.T) {
	h := newTestHandler(t)
	rec := doReq(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/uuid/approve", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without a token, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthAndPublicRoutesAreRegistered(t *testing.T) {
	h := newTestHandler(t)
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/auth/register"},
		{http.MethodPost, "/api/v1/auth/login"},
		{http.MethodPost, "/api/v1/auth/refresh"},
		{http.MethodPost, "/api/v1/auth/logout"},
		{http.MethodPost, "/api/v1/auth/verify"},
		{http.MethodGet, "/api/v1/venues"},
		{http.MethodGet, "/api/v1/categories"},
		{http.MethodGet, "/api/v1/events"},
		// A well-formed but nonexistent UUID: its route is registered, so the
		// request must reach the handler (a DB-backed 500 today) rather than
		// the mux's default 404. Malformed ids are no longer usable here —
		// they are a legitimate handler-level 404 now.
		{http.MethodGet, "/api/v1/events/9f5f0a2c-0000-0000-0000-000000000001"},
	} {
		rec := doReq(t, h, tc.method, tc.path, "")
		// A registered route must never 404 — it falls through to handler/auth
		// errors (400/401/...) or a DB-backed 500, regardless of the input.
		if rec.Code == http.StatusNotFound {
			t.Fatalf("%s %s: expected route to be registered, got 404", tc.method, tc.path)
		}
	}
}

func TestDeprecatedVerifyGETReturns405(t *testing.T) {
	h := newTestHandler(t)
	rec := doReq(t, h, http.MethodGet, "/api/v1/auth/verify?code=123456", "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 (route removed), got %d", rec.Code)
	}
}
