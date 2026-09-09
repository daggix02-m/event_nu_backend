package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func rlsTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		dir, _ := os.Getwd()
		for {
			p := filepath.Join(dir, ".env")
			if _, err := os.Stat(p); err == nil {
				_ = godotenv.Load(p)
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping RLS integration tests")
	}
	pool, err := database.NewPool(context.Background(), dsn)
	if err != nil {
		t.Skipf("database unreachable (%v)", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// rlsServicePool connects with the trusted 'service' role so we can seed rows
// (registration/login run under this role in production).
func rlsServicePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	pool, err := database.NewPoolWithRole(context.Background(), dsn, "service")
	if err != nil {
		t.Skipf("database unreachable (%v)", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedRLSUser(t *testing.T, pool *pgxpool.Pool) *string {
	t.Helper()
	users := NewUserRepository(pool)
	email := fmt.Sprintf("rls+%d@test.example", time.Now().UnixNano())
	user, err := users.Create(context.Background(), email, "x", "rls"+fmt.Sprint(time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return &user.ID
}

// TestRLSBlocksCrossUserRead proves the RLS backstop: even if application
// authorization were buggy and tried to fetch another user's row, the row is
// invisible under the requester's identity.
func TestRLSBlocksCrossUserRead(t *testing.T) {
	pool := rlsTestPool(t)
	seedPool := rlsServicePool(t)

	aID := seedRLSUser(t, seedPool)
	bID := seedRLSUser(t, seedPool)

	users := NewUserRepository(pool)

	// Simulate a request authenticated as user A: request tx + RLS context.
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(ctx)

	rctx := database.ContextWithTx(ctx, tx)
	if err := database.SetRLSContextTx(rctx, tx, *aID, "user"); err != nil {
		t.Fatalf("set rls context: %v", err)
	}

	// A can read their own row.
	if _, err := users.GetByID(rctx, *aID); err != nil {
		t.Fatalf("self read should succeed: %v", err)
	}

	// A cannot read B's row — must surface as not-found, not a leak.
	_, err = users.GetByID(rctx, *bID)
	if !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("cross-user read must be blocked (ErrNotFound), got %v", err)
	}
}

// TestRLSBlocksCrossUserSessionRead is the same proof for sessions: user A
// cannot see user B's refresh session.
func TestRLSBlocksCrossUserSessionRead(t *testing.T) {
	pool := rlsTestPool(t)
	seedPool := rlsServicePool(t)

	aID := seedRLSUser(t, seedPool)
	bID := seedRLSUser(t, seedPool)

	sessions := NewSessionRepository(seedPool)
	for _, uid := range []string{*aID, *bID} {
		if err := sessions.Create(context.Background(), &domain.Session{
			UserID:      uid,
			RefreshHash: uid + "-hash",
			ExpiresAt:   time.Now().Add(24 * time.Hour),
		}); err != nil {
			t.Fatalf("seed session: %v", err)
		}
	}

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(ctx)

	rctx := database.ContextWithTx(ctx, tx)
	if err := database.SetRLSContextTx(rctx, tx, *aID, "user"); err != nil {
		t.Fatalf("set rls context: %v", err)
	}

	// A cannot see B's session row by hash.
	_, err = sessions.GetActiveByRefreshHash(rctx, *bID+"-hash")
	if !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("cross-user session read must be blocked, got %v", err)
	}
}
