package repository

import (
	"context"
	"testing"

	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/database"
)

func setupRLSPrefs(t *testing.T) (string, string) {
	t.Helper()
	pool := rlsServicePool(t)
	cats := NewCategoryPreferenceRepository(pool)

	var categoryID string
	err := pool.QueryRow(context.Background(), "SELECT id::text FROM categories WHERE is_active = true ORDER BY sort_order, name LIMIT 1").Scan(&categoryID)
	if err != nil {
		t.Fatalf("fetch active category: %v", err)
	}

	aID := seedRLSUser(t, pool)
	bID := seedRLSUser(t, pool)

	ctx := context.Background()
	if _, err := cats.Replace(ctx, *aID, []string{categoryID}); err != nil {
		t.Fatalf("seed preferences for A: %v", err)
	}
	if _, err := cats.Replace(ctx, *bID, []string{categoryID}); err != nil {
		t.Fatalf("seed preferences for B: %v", err)
	}
	return *aID, *bID
}

// TestRLSPreferencesOwnRowOnly proves user A cannot read or replace user B's
// category preferences even if application authorization tried.
func TestRLSPreferencesOwnRowOnly(t *testing.T) {
	pool := rlsTestPool(t)
	aID, bID := setupRLSPrefs(t)

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(ctx)

	rctx := database.ContextWithTx(ctx, tx)
	if err := database.SetRLSContextTx(rctx, tx, aID, "user"); err != nil {
		t.Fatalf("set rls context: %v", err)
	}

	cats := NewCategoryPreferenceRepository(pool)

	// A sees their own ids.
	ids, _, err := cats.Get(rctx, aID)
	if err != nil {
		t.Fatalf("self get: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected A to have 1 category id, got %v", ids)
	}

	// A's Get of B's id must come back empty (row invisible), never an error.
	ids, _, err = cats.Get(rctx, bID)
	if err != nil {
		t.Fatalf("cross-user get must not error: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("cross-user get must yield no rows, got %v", ids)
	}

	// A cannot overwrite B's row either.
	if _, err := cats.Replace(rctx, bID, []string{"00000000-0000-0000-0000-000000000000"}); err == nil {
		t.Fatal("cross-user replace must be blocked by RLS")
	}
}

// TestRLSPreferencesStoredOnServiceSeededRow verifies seeding happened under
// the service role (the harness relies on it).
func TestRLSPreferencesStoredOnServiceSeededRow(t *testing.T) {
	pool := rlsTestPool(t)
	aID, _ := setupRLSPrefs(t)

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(ctx)

	rctx := database.ContextWithTx(ctx, tx)
	if err := database.SetRLSContextTx(rctx, tx, aID, "user"); err != nil {
		t.Fatalf("set rls context: %v", err)
	}
	ids, _, err := NewCategoryPreferenceRepository(pool).Get(rctx, aID)
	if err != nil {
		t.Fatalf("self get: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected seeded preference visible to owner, got %v", ids)
	}
}
