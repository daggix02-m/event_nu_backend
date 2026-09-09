package database

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// stubTx satisfies pgx.Tx via an embedded interface so context plumbing can be
// tested without a real database transaction (no methods are ever invoked).
type stubTx struct{ pgx.Tx }

// errQuerier is a Querier whose Exec always fails, for covering error paths
// without a database.
type errQuerier struct{}

func (errQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("injected exec failure")
}

func (errQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	panic("not used")
}

func (errQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
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

func TestContextWithTxRoundTrip(t *testing.T) {
	ctx := context.Background()
	if _, ok := TxFromContext(ctx); ok {
		t.Fatal("empty context must not carry a transaction")
	}

	tx := stubTx{}
	got, ok := TxFromContext(ContextWithTx(ctx, tx))
	if !ok {
		t.Fatal("expected the stored transaction to be returned")
	}
	if got != tx {
		t.Fatalf("expected the same tx back, got %v", got)
	}
}

func TestTxFromContextIgnoresNonTxValues(t *testing.T) {
	ctx := context.WithValue(context.Background(), txKey{}, "not-a-tx")
	if _, ok := TxFromContext(ctx); ok {
		t.Fatal("a non-tx value must not be reported as a transaction")
	}
}

func TestQuerierFromContextPrefersTx(t *testing.T) {
	tx := stubTx{}
	q := QuerierFromContext(ContextWithTx(context.Background(), tx), nil)
	if q != tx {
		t.Fatalf("expected the request tx to win over the pool, got %T", q)
	}
}

func TestQuerierFromContextFallsBackToPool(t *testing.T) {
	var pool pgxpool.Pool
	q := QuerierFromContext(context.Background(), &pool)
	if q != &pool {
		t.Fatalf("expected the pool fallback, got %T", q)
	}
}

func TestTxReturnsExistingTxUnowned(t *testing.T) {
	tx := stubTx{}
	got, owned, err := Tx(ContextWithTx(context.Background(), tx), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owned {
		t.Fatal("a context tx must not be owned by the caller")
	}
	if got != tx {
		t.Fatalf("expected the context tx, got %v", got)
	}
}

func TestNewPoolRejectsInvalidDSN(t *testing.T) {
	_, err := NewPool(context.Background(), "://not a dsn")
	if err == nil {
		t.Fatal("expected a parse error for an invalid DSN")
	}
}

func TestNewPoolWithRoleRejectsInvalidDSN(t *testing.T) {
	_, err := NewPoolWithRole(context.Background(), "://not a dsn", "service")
	if err == nil {
		t.Fatal("expected a parse error for an invalid DSN")
	}
}

// unpickableDSN points at a port that refuses connections immediately, so the
// lazy pool construction succeeds and the connectivity ping fails fast.
const unpickableDSN = "postgres://u:p@127.0.0.1:1/db?sslmode=disable"

func TestNewPoolPingFailure(t *testing.T) {
	_, err := NewPool(context.Background(), unpickableDSN)
	if err == nil {
		t.Fatal("expected a ping failure for an unreachable endpoint")
	}
}

func TestNewPoolWithRolePingFailure(t *testing.T) {
	_, err := NewPoolWithRole(context.Background(), unpickableDSN, "service")
	if err == nil {
		t.Fatal("expected a ping failure for an unreachable endpoint")
	}
}

// Integration tests below exercise the success paths; they skip when no
// DATABASE_URL is configured (local, CI without a DB).
func TestNewPoolConnectsAndOwnedTxCommits(t *testing.T) {
	loadEnv(t)
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping pool integration test")
	}

	ctx := context.Background()
	pool, err := NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)

	var one int
	if err := pool.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if one != 1 {
		t.Fatalf("expected 1, got %d", one)
	}

	// A fresh Tx on the pool is owned by the caller and must be rolled back.
	tx, owned, err := Tx(ctx, pool)
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}
	if !owned {
		t.Fatal("expected caller ownership for a fresh transaction")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
}

func TestNewPoolWithRoleAppliesRLSRole(t *testing.T) {
	loadEnv(t)
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping role integration test")
	}

	ctx := context.Background()
	pool, err := NewPoolWithRole(ctx, dsn, "service")
	if err != nil {
		t.Fatalf("NewPoolWithRole: %v", err)
	}
	t.Cleanup(pool.Close)

	var role string
	if err := pool.QueryRow(ctx, `SELECT current_setting('app.role')`).Scan(&role); err != nil {
		t.Fatalf("read app.role: %v", err)
	}
	if role != "service" {
		t.Fatalf("expected RLS role 'service' on pooled connections, got %q", role)
	}
}

func TestSetRLSContextSucceeds(t *testing.T) {
	loadEnv(t)
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping RLS integration test")
	}

	ctx := context.Background()
	pool, err := NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := SetRLSContext(ctx, pool, "user-1", "admin"); err != nil {
		t.Fatalf("SetRLSContext: %v", err)
	}
}

func TestSetRLSContextPropagatesExecError(t *testing.T) {
	loadEnv(t)
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping RLS integration test")
	}

	ctx := context.Background()
	pool, err := NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)

	// A canceled call context makes the Exec fail immediately.
	callCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := SetRLSContext(callCtx, pool, "user-1", "admin"); err == nil {
		t.Fatal("expected an error for a canceled context")
	}
}

func TestSetRLSContextTxMakesSettingsVisible(t *testing.T) {
	loadEnv(t)
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping RLS integration test")
	}

	ctx := context.Background()
	pool, err := NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	if err := SetRLSContextTx(ctx, tx, "user-7", "admin"); err != nil {
		t.Fatalf("SetRLSContextTx: %v", err)
	}

	var userID, role string
	if err := tx.QueryRow(ctx, `SELECT current_setting('app.user_id'), current_setting('app.role')`).
		Scan(&userID, &role); err != nil {
		t.Fatalf("read RLS settings: %v", err)
	}
	if userID != "user-7" || role != "admin" {
		t.Fatalf("expected RLS user-7/admin in tx, got %q/%q", userID, role)
	}
}

func TestSetRLSContextTxPropagatesExecError(t *testing.T) {
	err := SetRLSContextTx(context.Background(), errQuerier{}, "user-1", "admin")
	if err == nil {
		t.Fatal("expected the querier exec failure to propagate")
	}
}
