package repository

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedSyncEvent inserts a published, user-visible event (plus its organizer and
// venue) directly with the trusted 'service' role, bypassing the endpoints.
func seedSyncEvent(t *testing.T, pool *pgxpool.Pool) (eventID string) {
	t.Helper()
	ctx := context.Background()
	unix := fmt.Sprint(time.Now().UnixNano())

	var orgID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO organizers (slug, name) VALUES ($1, 'Sync Seed Org') RETURNING id`, "syncorg"+unix).Scan(&orgID); err != nil {
		t.Fatalf("seed organizer: %v", err)
	}
	var venueID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO venues (organizer_id, name, latitude, longitude) VALUES ($1, 'Sync Venue', 0, 0) RETURNING id`, orgID).Scan(&venueID); err != nil {
		t.Fatalf("seed venue: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO events (organizer_id, venue_id, title, description, price_display, action_target, starts_at, price_is_free, status, moderation_status)
		VALUES ($1, $2, $3, '', '', '', now() + interval '1 day', true, 'published', 'clean')
		RETURNING id`, orgID, venueID, "sync event "+unix).Scan(&eventID); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	return eventID
}

// TestSyncRepositoryDeltaScansUnderRLS proves the delta scans run through RLS
// (soft-deleted rows are invisible to the upsert query) while the privileged
// sync_deleted_ids helper surfaces exactly those ids to the same caller, and
// that the whitelist rejects comment (hard-delete) domains.
func TestSyncRepositoryDeltaScansUnderRLS(t *testing.T) {
	pool := rlsTestPool(t)
	seedPool := rlsServicePool(t)

	// Seeds are inserted on the service-pool connection directly, so they must
	// be removed after the test to keep the shared DB hermetic (a leftover
	// seed with a NULL description column breaks the plain-string scans in the
	// events list handlers). events/venues have no DELETE RLS policy, so the
	// cleanup runs through the admin (BYPASSRLS) URL.
	t.Cleanup(func() {
		adminDSN := os.Getenv("DATABASE_ADMIN_URL")
		if adminDSN == "" {
			t.Errorf("cleanup skipped: DATABASE_ADMIN_URL not set")
			return
		}
		admin, err := database.NewPool(context.Background(), adminDSN)
		if err != nil {
			t.Errorf("admin pool: %v", err)
			return
		}
		defer admin.Close()
		ctx := context.Background()
		for _, q := range []string{
			`DELETE FROM events WHERE organizer_id IN (SELECT id FROM organizers WHERE slug LIKE 'syncorg%')`,
			`DELETE FROM venues WHERE organizer_id IN (SELECT id FROM organizers WHERE slug LIKE 'syncorg%')`,
			`DELETE FROM organizers WHERE slug LIKE 'syncorg%'`,
		} {
			if _, err := admin.Exec(ctx, q); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
	})

	userID := seedRLSUser(t, seedPool)
	since := time.Now().UTC()

	visID := seedSyncEvent(t, seedPool)
	delA := seedSyncEvent(t, seedPool)
	delB := seedSyncEvent(t, seedPool)
	ctx := context.Background()
	for _, id := range []string{delA, delB} {
		if _, err := seedPool.Exec(ctx,
			`UPDATE events SET deleted_at = now(), updated_at = now() WHERE id = $1`, id); err != nil {
			t.Fatalf("soft-delete seed event: %v", err)
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(ctx)
	rctx := database.ContextWithTx(ctx, tx)
	if err := database.SetRLSContextTx(rctx, tx, *userID, "user"); err != nil {
		t.Fatalf("set rls context: %v", err)
	}

	sync := NewSyncRepository(pool)

	rows, err := sync.EventsChangedSince(rctx, since, 10)
	if err != nil {
		t.Fatalf("events changed since: %v", err)
	}
	if rows.HasMore {
		t.Fatalf("unexpected has_more in a 1-row window")
	}
	foundVis := false
	for _, e := range rows.Rows {
		switch e.ID {
		case visID:
			foundVis = true
		case delA, delB:
			t.Fatalf("soft-deleted event leaked through EventsChangedSince: %s", e.ID)
		}
	}
	if !foundVis {
		t.Fatalf("visible event %s missing from delta", visID)
	}

	deleted, err := sync.DeletedIDsChangedSince(rctx, since, "events", 10)
	if err != nil {
		t.Fatalf("deleted ids: %v", err)
	}
	if deleted.HasMore || len(deleted.Rows) != 2 {
		t.Fatalf("expected exactly 2 deleted ids (more=%v), got %d", deleted.HasMore, len(deleted.Rows))
	}
	byID := map[string]bool{}
	for _, d := range deleted.Rows {
		byID[d.ID] = true
		if d.ChangedAt.Before(since) {
			t.Fatalf("deleted change timestamp before cursor: %s", d.ChangedAt)
		}
	}
	if !byID[delA] || !byID[delB] {
		t.Fatalf("deleted ids missing: %s %s (got %v)", delA, delB, byID)
	}

	paged, err := sync.DeletedIDsChangedSince(rctx, since, "events", 1)
	if err != nil {
		t.Fatalf("deleted ids paged: %v", err)
	}
	if len(paged.Rows) != 1 || !paged.HasMore {
		t.Fatalf("limit=1 should page with has_more, got %d rows, more=%v", len(paged.Rows), paged.HasMore)
	}

	if _, err := sync.DeletedIDsChangedSince(rctx, since, "comments", 10); err == nil {
		t.Fatalf("expected whitelist rejection for 'comments' (hard-deleted domain)")
	}
}
