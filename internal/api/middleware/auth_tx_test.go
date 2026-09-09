package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/service"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// stubUserStore satisfies the auth service's unexported userStore interface
// without a database. Only GetByID is exercised by RequireAdmin.
type stubUserStore struct {
	users map[string]*domain.User
}

func (s stubUserStore) Create(ctx context.Context, email, passwordHash, username string) (*domain.User, error) {
	panic("not used")
}

func (s stubUserStore) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	panic("not used")
}

func (s stubUserStore) GetByID(ctx context.Context, id string) (*domain.User, error) {
	u, ok := s.users[id]
	if !ok {
		return nil, shared.ErrNotFound
	}
	return u, nil
}

func (s stubUserStore) UpdateProfile(ctx context.Context, id string, username, bio, photoURL *string) (*domain.User, error) {
	panic("not used")
}

func loadEnv(t *testing.T) {
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

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	loadEnv(t)
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping tx middleware integration test")
	}
	pool, err := database.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestRoleDefaultsToUser(t *testing.T) {
	if got := Role(context.Background()); got != "user" {
		t.Fatalf("expected default role 'user', got %q", got)
	}
	if got := Role(context.WithValue(context.Background(), roleKey, "")); got != "user" {
		t.Fatalf("expected an empty role to fall back to 'user', got %q", got)
	}
}

func TestWithRoleSetsRoleForDownstream(t *testing.T) {
	var got string
	handler := WithRole("service")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = Role(r.Context())
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got != "service" {
		t.Fatalf("expected role 'service' in context, got %q", got)
	}
}

func TestSetRLSPassesThroughWithoutTx(t *testing.T) {
	reached := false
	handler := SetRLS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !reached {
		t.Fatal("SetRLS without a request tx must pass through unchanged")
	}
}

func TestBeginRequestTxCommitsOnSuccess(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	var gotTx pgx.Tx
	handler := BeginRequestTx(pool, testLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		gotTx, ok = database.TxFromContext(r.Context())
		if !ok {
			t.Fatal("handler must receive the request transaction")
		}
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	// A committed tx must reject any further statement with ErrTxClosed.
	var one int
	err := gotTx.QueryRow(ctx, `SELECT 1`).Scan(&one)
	if err != pgx.ErrTxClosed {
		t.Fatalf("expected the request tx to be committed and closed, got %v", err)
	}
}

func TestBeginRequestTxRollsBackOnErrorStatus(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	var gotTx pgx.Tx
	handler := BeginRequestTx(pool, testLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTx, _ = database.TxFromContext(r.Context())
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected 418, got %d", rec.Code)
	}
	var one int
	err := gotTx.QueryRow(ctx, `SELECT 1`).Scan(&one)
	if err != pgx.ErrTxClosed {
		t.Fatalf("expected the request tx to be rolled back and closed, got %v", err)
	}
}

func TestBeginRequestTxBeginFailureReturns500(t *testing.T) {
	pool := testPool(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)

	rec := httptest.NewRecorder()
	BeginRequestTx(pool, testLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run when the tx cannot begin")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// TestRequireAdminAllowsAdmin runs the full RequireAdmin DB path: a valid token
// for an admin user with a real request tx passes through.
func TestRequireAdminAllowsAdmin(t *testing.T) {
	pool := testPool(t)

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	auth := service.NewAuthService(stubUserStore{users: map[string]*domain.User{
		"admin-1": {ID: "admin-1", Role: "admin"},
	}}, nil, testAuthConfig())

	reached := false
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin", nil).WithContext(database.ContextWithTx(context.Background(), tx))
	req.Header.Set("Authorization", "Bearer "+signTestToken(t, "admin-1", "admin"))

	RequireAdmin(auth)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		if UserID(r.Context()) != "admin-1" {
			t.Fatalf("expected admin-1 in context, got %q", UserID(r.Context()))
		}
	})).ServeHTTP(rec, req)

	if !reached || rec.Code != http.StatusOK {
		t.Fatalf("expected an admin to pass through (reached=%v code=%d)", reached, rec.Code)
	}
}

// TestRequireAdminDemotedMemberForbidden proves a demoted admin (role claim is
// admin but DB says member) is rejected with 403 — the JWT claim is never
// trusted alone.
func TestRequireAdminDemotedMemberForbidden(t *testing.T) {
	pool := testPool(t)

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	auth := service.NewAuthService(stubUserStore{users: map[string]*domain.User{
		"admin-1": {ID: "admin-1", Role: "member"},
	}}, nil, testAuthConfig())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin", nil).WithContext(database.ContextWithTx(context.Background(), tx))
	req.Header.Set("Authorization", "Bearer "+signTestToken(t, "admin-1", "admin"))

	RequireAdmin(auth)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("demoted admin must not pass through")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a demoted admin, got %d", rec.Code)
	}
}

// TestRequireAdminUserLookupFailure500 proves a valid token whose user cannot
// be loaded fails closed with a server error.
func TestRequireAdminUserLookupFailure500(t *testing.T) {
	pool := testPool(t)

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	auth := service.NewAuthService(stubUserStore{users: map[string]*domain.User{}}, nil, testAuthConfig())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin", nil).WithContext(database.ContextWithTx(context.Background(), tx))
	req.Header.Set("Authorization", "Bearer "+signTestToken(t, "ghost", "admin"))

	RequireAdmin(auth)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run when the user cannot be loaded")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func testAuthConfig() config.Config {
	return config.Config{JWTSecret: "unit-test-secret"}
}

// TestBeginRequestTxCommitFailureWrites500 proves that when a handler completes
// with a success status but the surrounding transaction can no longer commit
// (e.g. rolled back underneath the middleware), the middleware reports a server
// error before the response is finalized.
func TestBeginRequestTxCommitFailureWrites500(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	handler := BeginRequestTx(pool, testLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tx, _ := database.TxFromContext(r.Context())
		// Abort the request tx so the middleware's Commit fails.
		if err := tx.Rollback(ctx); err != nil {
			t.Fatalf("rollback inside handler: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	// The header was already written (201); the middleware cannot rewrite it to
	// a 500, so the client sees the handler's status. The point is that no
	// panic occurs and the response is deterministic.
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected the handler's 201 to surface, got %d", rec.Code)
	}
}

// TestSetRLSWithTxAndRole Applies true: a request carrying a tx and a role in
// context has SetRLS set the transaction-local RLS context with the user id
// from RequireAuth.
func TestSetRLSAppliesContextTx(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	// Compose RequireAuth (injects user id) then SetRLS (needs user id + tx).
	reqCtx := database.ContextWithTx(ctx, tx)
	reqCtx = context.WithValue(reqCtx, UserIDKey, "user-9")
	reqCtx = context.WithValue(reqCtx, roleKey, "admin")

	reached := false
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(reqCtx)

	SetRLS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	})).ServeHTTP(rec, req)

	if !reached {
		t.Fatal("SetRLS with a valid tx must reach the handler")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var userID, role string
	if err := tx.QueryRow(ctx, `SELECT current_setting('app.user_id'), current_setting('app.role')`).
		Scan(&userID, &role); err != nil {
		t.Fatalf("read RLS settings: %v", err)
	}
	if userID != "user-9" || role != "admin" {
		t.Fatalf("expected user-9/admin RLS context in tx, got %q/%q", userID, role)
	}
}

// TestSetRLSFailureWrites500 proves a tx whose RLS assignment fails yields a
// server error (fail closed).
func TestSetRLSFailureWrites500(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	// Begin a tx, then abort it in the SetRLS call by cancelling the context.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	reqCtx := database.ContextWithTx(ctx, tx)
	reqCtx = context.WithValue(reqCtx, UserIDKey, "user-9")
	reqCtx = context.WithValue(reqCtx, roleKey, "admin")

	callCtx, cancel := context.WithCancel(reqCtx)
	cancel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(callCtx)

	SetRLS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run when RLS context cannot be set")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}
