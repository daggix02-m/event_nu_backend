package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedSession creates a single active session with a known hash for a user,
// using the trusted service pool so the row is visible to requests.
func seedSession(t *testing.T, pool *pgxpool.Pool, userID, hash string) {
	t.Helper()
	sessions := NewSessionRepository(pool)
	if err := sessions.Create(context.Background(), &domain.Session{
		UserID:      userID,
		RefreshHash: hash,
		ExpiresAt:   time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

// TestConsumeForRotationConcurrent proves that two concurrent transactions
// consuming the same active refresh token yield exactly one winner: one returns
// the session, the other returns ErrNotFound, and the row is revoked.
//
// Each goroutine mirrors the production tx lifecycle (commit the winner,
// roll back the loser) so neither holds the row lock while the other waits;
// Postgres serializes the UPDATEs and re-evaluates the WHERE clause after the
// winner commits, making the loser match zero rows.
func TestConsumeForRotationConcurrent(t *testing.T) {
	seedPool := rlsServicePool(t)
	pool := rlsServicePool(t)

	userID := seedRLSUser(t, seedPool)
	hash := fmt.Sprintf("rotate-%d-hash", time.Now().UnixNano())
	seedSession(t, seedPool, *userID, hash)

	sessions := NewSessionRepository(pool)

	ctx := context.Background()

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		winners  int
		notFound int
		errs     []error
	)

	// run consumes the token in its own tx and immediately settles it the way
	// the request-tx middleware would (commit on success, rollback otherwise).
	run := func() {
		defer wg.Done()
		tx, err := pool.Begin(ctx)
		if err != nil {
			mu.Lock()
			errs = append(errs, fmt.Errorf("begin tx: %w", err))
			mu.Unlock()
			return
		}
		defer tx.Rollback(ctx)

		_, err = sessions.ConsumeForRotation(database.ContextWithTx(ctx, tx), hash)
		mu.Lock()
		switch {
		case err == nil:
			winners++
		case errors.Is(err, shared.ErrNotFound):
			notFound++
		default:
			errs = append(errs, err)
		}
		mu.Unlock()

		if err == nil {
			if cerr := tx.Commit(ctx); cerr != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("commit: %w", cerr))
				mu.Unlock()
			}
		}
	}

	wg.Add(2)
	go run()
	go run()
	wg.Wait()

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if winners != 1 {
		t.Fatalf("expected exactly 1 winner, got %d", winners)
	}
	if notFound != 1 {
		t.Fatalf("expected exactly 1 not-found, got %d", notFound)
	}

	// The row must now be revoked: a further lookup must find nothing.
	verifier := NewSessionRepository(pool)
	if _, err := verifier.GetActiveByRefreshHash(context.Background(), hash); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("expected row revoked (not found after rotation), got %v", err)
	}
}
