package repository

import (
	"context"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/domain"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/database"
)

// TestDomainEventsRLSAndRelay is DB-gated: it proves the transactional
// guarantee lives at the SQL boundary too — a fact written inside a user's
// RLS transaction survives commit, is invisible to other users, and the
// service-role relay can claim and finalize it.
func TestDomainEventsRLSAndRelay(t *testing.T) {
	pool := rlsTestPool(t)        // request-path pool (simulated user via per-tx RLS)
	seedPool := rlsServicePool(t) // seeding + the relay's role in production

	ctx := context.Background()
	ownerID := seedRLSUser(t, seedPool)

	// Cancel any rows left from earlier runs sharing the key namespace.
	key := "e-outbox-" + time.Now().Format("150405.000000000")
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cctx, `DELETE FROM domain_events WHERE owner_user_id = $1`, *ownerID)
	})

	// The production request path: request transaction + RLS context, then
	// the fact write rides the SAME transaction that changed the aggregate.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin request tx: %v", err)
	}
	tctx := database.ContextWithTx(ctx, tx)
	if err := database.SetRLSContextTx(tctx, tx, *ownerID, "user"); err != nil {
		t.Fatalf("set RLS: %v", err)
	}
	repo := NewDomainEventRepository(pool)
	ev := domain.DomainEvent{
		Name:       "event.published",
		Key:        key,
		OccurredAt: time.Now().UTC(),
		Payload:    []byte(`{"event_id":"` + key + `"}`),
	}
	if err := repo.Write(tctx, ev, *ownerID); err != nil {
		t.Fatalf("write fact in request tx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Another user must not see the fact row.
	otherTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin other-user tx: %v", err)
	}
	octx := database.ContextWithTx(ctx, otherTx)
	if err := database.SetRLSContextTx(octx, otherTx, "22222222-2222-4222-8222-222222222222", "user"); err != nil {
		t.Fatalf("set RLS (other user): %v", err)
	}
	var n int
	if err := repo.q(octx).QueryRow(octx, `SELECT count(*) FROM domain_events WHERE key = $1`, key).Scan(&n); err != nil {
		t.Fatalf("count as other user: %v", err)
	}
	if err := otherTx.Rollback(ctx); err != nil {
		t.Fatalf("rollback other: %v", err)
	}
	if n != 0 {
		t.Fatalf("other user saw %d rows owned by someone else — RLS leak", n)
	}

	// The service-role relay claims it (attempts bumps to 1), then finalizes;
	// sent rows are never re-claimed.
	relayRepo := NewDomainEventRepository(seedPool)
	claims, err := relayRepo.ClaimBatch(ctx, 50, 5*time.Second)
	if err != nil {
		t.Fatalf("relay claim: %v", err)
	}
	var found *domain.DomainEventDelivery
	for i := range claims {
		if claims[i].Event.Key == key {
			found = &claims[i]
		}
	}
	if found == nil {
		t.Fatalf("claimed batch missing the fact row (got %d rows)", len(claims))
	}
	if found.Attempts != 1 {
		t.Fatalf("attempts: got %d, want 1 after first claim", found.Attempts)
	}
	if err := relayRepo.MarkSent(ctx, found.ID); err != nil {
		t.Fatalf("mark sent: %v", err)
	}

	// The owner still sees their (now sent) row through RLS.
	ownTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin owner tx: %v", err)
	}
	wctx := database.ContextWithTx(ctx, ownTx)
	if err := database.SetRLSContextTx(wctx, ownTx, *ownerID, "user"); err != nil {
		t.Fatalf("set RLS (owner): %v", err)
	}
	if err := repo.q(wctx).QueryRow(wctx, `SELECT count(*) FROM domain_events WHERE key = $1 AND status = 'sent'`, key).Scan(&n); err != nil {
		t.Fatalf("owner count: %v", err)
	}
	if err := ownTx.Rollback(ctx); err != nil {
		t.Fatalf("rollback owner: %v", err)
	}
	if n != 1 {
		t.Fatalf("owner saw %d sent rows, want 1", n)
	}

	after, err := relayRepo.ClaimBatch(ctx, 50, 5*time.Second)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	for _, c := range after {
		if c.Event.Key == key {
			t.Fatal("sent row was re-claimed")
		}
	}
}
