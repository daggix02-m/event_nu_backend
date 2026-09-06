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

// seedOrganizerApplication creates a pending application for the given user.
func seedOrganizerApplication(t *testing.T, pool *pgxpool.Pool, userID string) string {
	t.Helper()
	orgs := NewOrganizerRepository(pool)
	app := &domain.OrganizerApplication{
		UserID:         userID,
		RequestedName:  fmt.Sprintf("Seed Org %d", time.Now().UnixNano()),
		RequestedSlug:  fmt.Sprintf("seed-org-%d", time.Now().UnixNano()),
		Bio:            "seeded for approval tests",
		Status:         "pending",
		SupportingData: map[string]any{},
	}
	if err := orgs.CreateApplication(context.Background(), app); err != nil {
		t.Fatalf("seed application: %v", err)
	}
	return app.ID
}

// TestApproveApplicationConcurrent proves the WHERE status='pending' guard is
// atomic: two concurrent approvals run the guarded UPDATE, Postgres re-evaluates
// the predicate after the winner's commit, so exactly one wins and the loser
// surfaces a 409-typed conflict.
func TestApproveApplicationConcurrent(t *testing.T) {
	seedPool := rlsServicePool(t)
	pool := rlsServicePool(t)

	userID := seedRLSUser(t, seedPool)
	appID := seedOrganizerApplication(t, seedPool, *userID)

	orgs := NewOrganizerRepository(pool)
	adminID := seedRLSUser(t, seedPool)
	ctx := context.Background()

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners int
		losers  int
		errs    []error
	)

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

		_, err = orgs.ApproveApplication(database.ContextWithTx(ctx, tx), appID, *adminID, "approved")
		mu.Lock()
		switch {
		case err == nil:
			winners++
		case errors.Is(err, shared.ErrConflict):
			losers++
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
		t.Fatalf("expected exactly 1 winner, got %d (losers=%d)", winners, losers)
	}
	if losers != 1 {
		t.Fatalf("expected exactly 1 conflict loser, got %d", losers)
	}
}

// TestRejectApplicationNonPending ensures rejecting an already-reviewed
// application yields a 409-typed conflict, not a silent no-op.
func TestRejectApplicationNonPending(t *testing.T) {
	seedPool := rlsServicePool(t)
	pool := rlsServicePool(t)

	userID := seedRLSUser(t, seedPool)
	appID := seedOrganizerApplication(t, seedPool, *userID)
	adminID := seedRLSUser(t, seedPool)

	orgs := NewOrganizerRepository(pool)
	if err := orgs.RejectApplication(context.Background(), appID, *adminID, "no"); err != nil {
		t.Fatalf("reject pending: %v", err)
	}
	err := orgs.RejectApplication(context.Background(), appID, *adminID, "again")
	if !errors.Is(err, shared.ErrConflict) {
		t.Fatalf("reject non-pending: expected ErrConflict, got %v", err)
	}
}
