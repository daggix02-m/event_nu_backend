package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
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
	// Walk up to find the repo .env.
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

func newTestApp(t *testing.T) (*api.Application, http.Handler) {
	t.Helper()
	loadEnvForTest(t)

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping DB-backed auth integration tests")
	}

	pool, err := database.NewPool(context.Background(), dsn)
	if err != nil {
		t.Skipf("database unreachable (%v) — skipping DB-backed auth integration tests", err)
	}
	t.Cleanup(pool.Close)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	app := api.NewApp(testLogger(), cfg, pool)
	t.Cleanup(app.RateLimiter.Stop)
	return app, routes.New(app)
}

type authResponse struct {
	Data dto.AuthResponse `json:"data"`
}

func doJSON(t *testing.T, h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeAuth(t *testing.T, rec *httptest.ResponseRecorder) authResponse {
	t.Helper()
	var resp authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode auth response: %v; body=%s", err, rec.Body.String())
	}
	return resp
}

func registerUser(t *testing.T, h http.Handler, email string) authResponse {
	t.Helper()
	uname := "u" + strings.TrimPrefix(fmt.Sprintf("%d", time.Now().UnixNano()), "1")[:14]
	rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/register",
		fmt.Sprintf(`{"email":%q,"password":"password123","username":%q}`, email, uname), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("register: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	return decodeAuth(t, rec)
}

func TestAuthFlowMatrix(t *testing.T) {
	_, h := newTestApp(t)
	email := fmt.Sprintf("auth+%d@test.example", time.Now().UnixNano())

	// Register succeeds and returns a user + token pair.
	reg := registerUser(t, h, email)
	if reg.Data.User.Email != email {
		t.Fatalf("expected registered email, got %q", reg.Data.User.Email)
	}
	if reg.Data.AccessToken == "" || reg.Data.RefreshToken == "" {
		t.Fatal("expected access and refresh tokens")
	}

	// Duplicate registration conflicts.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/register",
		fmt.Sprintf(`{"email":%q,"password":"password123","username":"dup"}`, email), "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate register: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// Malformed JSON is a 400.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/register", `{"email":`, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed body: expected 400, got %d", rec.Code)
	}

	// Weak password is a 422.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/register",
		`{"email":"weak@test.example","password":"short","username":"weakuser"}`, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("weak password: expected 422, got %d", rec.Code)
	}

	// Login with correct credentials.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"email":%q,"password":"password123"}`, email), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	login := decodeAuth(t, rec)

	// Wrong password is rejected as 401 with generic message.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"email":%q,"password":"wrongpass123"}`, email), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: expected 401, got %d", rec.Code)
	}

	// Unknown email is also 401 (no user enumeration).
	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"email":"nobody@test.example","password":"password123"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown email: expected 401, got %d", rec.Code)
	}

	// /users/me unauthenticated is 401.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/users/me", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("me unauthenticated: expected 401, got %d", rec.Code)
	}

	// /users/me with a valid token returns the user.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/users/me", "", login.Data.AccessToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("me authenticated: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Refresh rotates the token pair.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/refresh",
		fmt.Sprintf(`{"refresh_token":%q}`, login.Data.RefreshToken), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	refreshed := decodeAuth(t, rec)
	if refreshed.Data.RefreshToken == login.Data.RefreshToken {
		t.Fatal("refresh must rotate the refresh token")
	}

	// Replaying the rotated refresh token is rejected.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/refresh",
		fmt.Sprintf(`{"refresh_token":%q}`, login.Data.RefreshToken), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("replay rotated token: expected 401, got %d", rec.Code)
	}

	// Garbage refresh token is rejected.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"garbage"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("garbage refresh: expected 401, got %d", rec.Code)
	}

	// Logout revokes the current refresh token.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/logout",
		fmt.Sprintf(`{"refresh_token":%q}`, refreshed.Data.RefreshToken), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("logout: expected 200, got %d", rec.Code)
	}

	// Using the logged-out refresh token fails.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/refresh",
		fmt.Sprintf(`{"refresh_token":%q}`, refreshed.Data.RefreshToken), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after logout: expected 401, got %d", rec.Code)
	}
}

// TestRefreshConcurrentSingleWinner proves refresh-token rotation is atomic at
// the HTTP layer: N concurrent refreshes of the same token yield exactly one
// success, and the consumed token can never be replayed afterwards.
func TestRefreshConcurrentSingleWinner(t *testing.T) {
	_, h := newTestApp(t)
	email := fmt.Sprintf("authrace+%d@test.example", time.Now().UnixNano())
	reg := registerUser(t, h, email)
	token := reg.Data.RefreshToken

	const n = 8
	var (
		wg           sync.WaitGroup
		mu           sync.Mutex
		okCount      int
		unauthorized int
		other        []int
		winnerBody   string
	)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/refresh",
				fmt.Sprintf(`{"refresh_token":%q}`, token), "")
			mu.Lock()
			defer mu.Unlock()
			switch rec.Code {
			case http.StatusOK:
				okCount++
				winnerBody = rec.Body.String()
			case http.StatusUnauthorized:
				unauthorized++
			default:
				other = append(other, rec.Code)
			}
		}()
	}
	wg.Wait()

	if okCount != 1 {
		t.Fatalf("expected exactly one 200, got %d (401s=%d, other=%v, winner=%s)",
			okCount, unauthorized, other, winnerBody)
	}
	if unauthorized != n-1 {
		t.Fatalf("expected %d unauthorized, got %d", n-1, unauthorized)
	}

	// The consumed token is revoked — replaying it must now fail.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/refresh",
		fmt.Sprintf(`{"refresh_token":%q}`, token), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("replay of consumed token: expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRegisterBoundaryValidation(t *testing.T) {
	_, h := newTestApp(t)

	// Boundary: exactly 8-char password and 3-char username pass.
	email := fmt.Sprintf("boundary+%d@test.example", time.Now().UnixNano())
	uname := fmt.Sprintf("u%d", time.Now().UnixNano()%1000)
	rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/register",
		fmt.Sprintf(`{"email":%q,"password":"12345678","username":%q}`, email, uname), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("boundary register: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Username too long.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/register",
		`{"email":"longuser@test.example","password":"12345678","username":"abcdefghijklmnopqrstuvwxyz123456789"}`, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("long username: expected 422, got %d", rec.Code)
	}

	// Empty body.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/register", "", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body: expected 400, got %d", rec.Code)
	}
}

// TestAuthLoginRateLimitIP proves the IP bucket trips independently of the
// account: burst logins from one IP across distinct accounts yield a 429 with a
// generic body and a Retry-After header.
func TestAuthLoginRateLimitIP(t *testing.T) {
	t.Setenv("AUTH_LOGIN_RATE_LIMIT", "3")
	t.Setenv("AUTH_LOGIN_RATE_WINDOW", "5m")
	_, h := newTestApp(t)

	for i := 0; i < 3; i++ {
		email := fmt.Sprintf("rl-ip-%d-%d@test.example", i, time.Now().UnixNano())
		rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/login",
			fmt.Sprintf(`{"email":%q,"password":"wrongpass123"}`, email), "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("login %d: expected 401, got %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}

	rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"email":"overflow@test.example","password":"wrongpass123"}`, "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on the 4th attempt, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on the 429")
	}
	body := rec.Body.String()
	if !strings.Contains(body, "too_many_requests") {
		t.Fatalf("expected generic too_many_requests body, got %s", body)
	}
	// The 429 must not reveal that overflow@test.example has no account.
	if strings.Contains(body, "overflow@test.example") || strings.Contains(body, "invalid_credentials") {
		t.Fatalf("429 body leaks account info: %s", body)
	}
}

// TestAuthLoginRateLimitAccount proves the account bucket independently guards
// one email: repeated logins for the same account from fresh IPs still trip.
func TestAuthLoginRateLimitAccount(t *testing.T) {
	t.Setenv("AUTH_LOGIN_RATE_LIMIT", "3")
	t.Setenv("AUTH_LOGIN_RATE_WINDOW", "5m")
	_, h := newTestApp(t)

	email := fmt.Sprintf("rl-acct-%d@test.example", time.Now().UnixNano())
	for i := 0; i < 3; i++ {
		rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/login",
			fmt.Sprintf(`{"email":%q,"password":"wrongpass123"}`, email), "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("login %d: expected 401, got %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(fmt.Sprintf(`{"email":%q,"password":"wrongpass123"}`, email)))
	req.Header.Set("X-Forwarded-For", "198.51.100.77")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 from the account bucket on a fresh IP, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on the 429")
	}
}

// TestAuthVerifyRateLimit proves the verify route is IP-rate-limited too.
func TestAuthVerifyRateLimit(t *testing.T) {
	t.Setenv("AUTH_VERIFY_RATE_LIMIT", "3")
	t.Setenv("AUTH_VERIFY_RATE_WINDOW", "1h")
	_, h := newTestApp(t)

	for i := 0; i < 3; i++ {
		rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/verify", `{"code":"000000"}`, "")
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("verify attempt %d: unexpected 429", i+1)
		}
	}
	rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/verify", `{"code":"000000"}`, "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on the 4th verify, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on the 429")
	}
	if !strings.Contains(rec.Body.String(), "too_many_requests") {
		t.Fatalf("expected generic too_many_requests body, got %s", rec.Body.String())
	}
}
