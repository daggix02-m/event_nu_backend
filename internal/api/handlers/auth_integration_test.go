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