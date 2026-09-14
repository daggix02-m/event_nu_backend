package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedPublishedEventWithRSVPs creates an organizer-owned published event with
// the given RSVPs (userID -> public flag; status always 'confirmed'). Returns
// the event id. Runs on the privileged service pool so RLS never interferes.
func seedPublishedEventWithRSVPs(t *testing.T, service *pgxpool.Pool, ownerID string, rsvps map[string]bool) string {
	t.Helper()
	ctx := context.Background()

	var organizerID string
	slug := fmt.Sprintf("momorg-%d", time.Now().UnixNano())
	if err := service.QueryRow(ctx, `
		INSERT INTO organizers (owner_user_id, slug, name)
		VALUES ($1, $2, $3) RETURNING id`, ownerID, slug, "Moments Org "+slug).
		Scan(&organizerID); err != nil {
		t.Fatalf("seed organizer: %v", err)
	}

	var eventID string
	if err := service.QueryRow(ctx, `
		INSERT INTO events (organizer_id, title, starts_at, status, moderation_status)
		VALUES ($1, $2, now() + interval '1 day', 'published', 'clean')
		RETURNING id`, organizerID, "Moments Event "+slug).Scan(&eventID); err != nil {
		t.Fatalf("seed event: %v", err)
	}

	for userID, public := range rsvps {
		if _, err := service.Exec(ctx, `
			INSERT INTO event_rsvps (event_id, user_id, status, public_rsvp)
			VALUES ($1, $2, 'confirmed', $3)`, eventID, userID, public); err != nil {
			t.Fatalf("seed rsvp: %v", err)
		}
	}
	return eventID
}

// seedReadyMediaAsset inserts a 'ready', non-deleted media asset owned by the
// given user and returns its id.
func seedReadyMediaAsset(t *testing.T, service *pgxpool.Pool, ownerID string) string {
	t.Helper()
	var id string
	err := service.QueryRow(context.Background(), `
		INSERT INTO media_assets (uploader_id, kind, storage_bucket, storage_key, content_type, status)
		VALUES ($1, 'event_gallery', 'ci', $2, 'image/png', 'ready')
		RETURNING id`, ownerID, fmt.Sprintf("ci/mom-%d.png", time.Now().UnixNano())).Scan(&id)
	if err != nil {
		t.Fatalf("seed media: %v", err)
	}
	return id
}

// TestMomentEligibilityChecks: HasRSVPOrTicket is true for a confirmed RSVP or
// an issued ticket, and false otherwise; IsValidMediaAsset requires a ready,
// owned, non-deleted asset.
func TestMomentEligibilityChecks(t *testing.T) {
	pool := rlsTestPool(t)
	service := rlsServicePool(t)

	aID := seedRLSUserNamed(t, service, "mom-elig-a")
	bID := seedRLSUserNamed(t, service, "mom-elig-b")
	eventID := seedPublishedEventWithRSVPs(t, service, aID, map[string]bool{aID: true})

	moments := NewMomentRepository(pool)

	txA := userContext(t, pool, aID)
	defer txA.Rollback(context.Background())
	ctxA := database.ContextWithTx(context.Background(), txA)

	if ok, err := moments.HasRSVPOrTicket(ctxA, aID, eventID); err != nil || !ok {
		t.Fatalf("RSVP holder must be eligible, got ok=%v err=%v", ok, err)
	}
	if ok, err := moments.HasRSVPOrTicket(ctxA, bID, eventID); err != nil || ok {
		t.Fatalf("non-attendee must not be eligible, got ok=%v err=%v", ok, err)
	}

	// Per-user tx for B's own identity (RSVP check is caller-scoped data).
	txB := userContext(t, pool, bID)
	defer txB.Rollback(context.Background())
	ctxB := database.ContextWithTx(context.Background(), txB)
	if ok, err := moments.HasRSVPOrTicket(ctxB, bID, eventID); err != nil || ok {
		t.Fatalf("ticket-less user must not be eligible, got ok=%v err=%v", ok, err)
	}

	// Issued ticket makes the user eligible (full chain: type -> order -> item).
	// Orders require user_id = current_app_user_id() while tickets require the
	// privileged role, so seed on a service pool tx carrying BOTH identities.
	ctx := context.Background()
	txSeed, err := service.Begin(ctx)
	if err != nil {
		t.Fatalf("seed tx: %v", err)
	}
	if err := database.SetRLSContextTx(ctx, txSeed, bID, "service"); err != nil {
		t.Fatalf("seed identity: %v", err)
	}
	var ticketTypeID, orderItemID string
	if err := txSeed.QueryRow(ctx, `
		INSERT INTO ticket_types (event_id, name, price_minor, currency)
		VALUES ($1, 'CI', 0, 'ETB') RETURNING id`, eventID).Scan(&ticketTypeID); err != nil {
		t.Fatalf("seed ticket type: %v", err)
	}
	if _, err := txSeed.Exec(ctx, `
		INSERT INTO orders (user_id, event_id, status, currency, subtotal_minor, total_minor)
		VALUES ($1, $2, 'paid', 'ETB', 0, 0)`, bID, eventID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if err := txSeed.QueryRow(ctx, `
		INSERT INTO order_items (order_id, ticket_type_id, quantity, currency, unit_price, subtotal)
		VALUES ((SELECT id FROM orders WHERE user_id = $1 AND event_id = $2 LIMIT 1), $3, 1, 'ETB', 0, 0)
		RETURNING id`, bID, eventID, ticketTypeID).Scan(&orderItemID); err != nil {
		t.Fatalf("seed order item: %v", err)
	}
	if _, err := txSeed.Exec(ctx, `
		INSERT INTO tickets (order_item_id, user_id, event_id, ticket_type_id, code_hash, status)
		VALUES ($1, $2, $3, $4, $5, 'issued')`,
		orderItemID, bID, eventID, ticketTypeID,
		fmt.Sprintf("chash-%d", time.Now().UnixNano())); err != nil {
		t.Fatalf("seed ticket: %v", err)
	}
	if err := txSeed.Commit(ctx); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
	if ok, err := moments.HasRSVPOrTicket(ctxB, bID, eventID); err != nil || !ok {
		t.Fatalf("ticket holder must be eligible, got ok=%v err=%v", ok, err)
	}

	// Media validation.
	ownAsset := seedReadyMediaAsset(t, service, aID)
	otherAsset := seedReadyMediaAsset(t, service, bID)
	if ok, err := moments.IsValidMediaAsset(ctxA, aID, ownAsset); err != nil || !ok {
		t.Fatalf("own ready asset must validate, got ok=%v err=%v", ok, err)
	}
	if ok, err := moments.IsValidMediaAsset(ctxA, aID, otherAsset); err != nil || ok {
		t.Fatalf("someone else's asset must not validate, got ok=%v err=%v", ok, err)
	}
}

// TestMomentAttendeesPrivacy: the attendee directory returns only public_rsvp
// confirmed/attended RSVPs of a published event, and never leaks RSVPs of an
// unpublished (draft) event — regardless of who queries (definer bypasses RLS
// but explicitly gates on event visibility).
func TestMomentAttendeesPrivacy(t *testing.T) {
	pool := rlsTestPool(t)
	service := rlsServicePool(t)

	ownerID := seedRLSUserNamed(t, service, "mom-att-owner")
	publicID := seedRLSUserNamed(t, service, "mom-att-pub")
	privateID := seedRLSUserNamed(t, service, "mom-att-priv")
	cancelledID := seedRLSUserNamed(t, service, "mom-att-can")

	eventID := seedPublishedEventWithRSVPs(t, service, ownerID, map[string]bool{
		publicID:    true,
		privateID:   false,
		cancelledID: true,
	})
	// Cancel cancelledID's RSVP (direct SQL on the service pool).
	if _, err := service.Exec(context.Background(), `
		UPDATE event_rsvps SET status = 'cancelled' WHERE user_id = $1 AND event_id = $2`,
		cancelledID, eventID); err != nil {
		t.Fatalf("cancel rsvp: %v", err)
	}

	moments := NewMomentRepository(pool)
	// Query as a random non-attendee user — definer must still return public rows.
	viewerID := seedRLSUserNamed(t, service, "mom-att-viewer")
	tx := userContext(t, pool, viewerID)
	defer tx.Rollback(context.Background())
	ctx := database.ContextWithTx(context.Background(), tx)

	attendees, err := moments.ListAttendees(ctx, eventID, 100, 0)
	if err != nil {
		t.Fatalf("list attendees: %v", err)
	}
	total, err := moments.CountAttendees(ctx, eventID)
	if err != nil {
		t.Fatalf("count attendees: %v", err)
	}
	if total != 1 || len(attendees) != 1 {
		t.Fatalf("expected 1 public attendee, got total=%d items=%d", total, len(attendees))
	}
	if attendees[0].UserID != publicID {
		t.Fatalf("expected %s as the public attendee, got %+v", publicID, attendees[0])
	}
	if attendees[0].DisplayName == "" {
		t.Fatalf("expected a display name on the attendee, got %+v", attendees[0])
	}

	// Draft event: directory must be empty (0 count, no rows). No leak.
	var draftEventID string
	if err := service.QueryRow(context.Background(), `
		INSERT INTO events (organizer_id, title, starts_at, status)
		VALUES ($1, $2, now() + interval '1 day', 'draft') RETURNING id`,
		func() string {
			var oid string
			err := service.QueryRow(context.Background(),
				`SELECT id FROM organizers WHERE owner_user_id = $1 LIMIT 1`, ownerID).Scan(&oid)
			if err != nil {
				t.Fatalf("fetch organizer: %v", err)
			}
			return oid
		}(), "Draft Moments "+fmt.Sprint(time.Now().UnixNano())).Scan(&draftEventID); err != nil {
		t.Fatalf("seed draft event: %v", err)
	}
	if _, err := service.Exec(context.Background(), `
		INSERT INTO event_rsvps (event_id, user_id, status, public_rsvp)
		VALUES ($1, $2, 'confirmed', true)`, draftEventID, publicID); err != nil {
		t.Fatalf("seed draft rsvp: %v", err)
	}
	// Events RLS hides the draft from the viewer, so the service-bypass path is
	// exercised: the definer must still refuse on its own event-visibility guard.
	total, err = moments.CountAttendees(ctx, draftEventID)
	if err != nil {
		t.Fatalf("count draft attendees: %v", err)
	}
	if total != 0 {
		t.Fatalf("draft event must expose no attendees, got %d", total)
	}
}

// TestMomentCreateDeleteRLS: a user creates a moment under their identity and
// another user cannot delete it at the SQL layer (RLS yields ErrNotFound), and
// listing only shows moments from published events.
func TestMomentCreateDeleteRLS(t *testing.T) {
	pool := rlsTestPool(t)
	service := rlsServicePool(t)

	aID := seedRLSUserNamed(t, service, "mom-cd-a")
	bID := seedRLSUserNamed(t, service, "mom-cd-b")
	eventID := seedPublishedEventWithRSVPs(t, service, aID, map[string]bool{aID: true})
	assetID := seedReadyMediaAsset(t, service, aID)

	moments := NewMomentRepository(pool)

	txA := userContext(t, pool, aID)
	ctxA := database.ContextWithTx(context.Background(), txA)
	moment, err := moments.Create(ctxA, &domain.Moment{
		EventID:      eventID,
		UserID:       aID,
		MediaAssetID: assetID,
		Caption:      "my moment",
	})
	if err != nil {
		t.Fatalf("create moment: %v", err)
	}
	if err := txA.Commit(context.Background()); err != nil {
		t.Fatalf("commit moment: %v", err)
	}

	// Owner sees it on their identity.
	if got, err := moments.ListByEvent(userContextCtx(t, pool, bID), eventID, 100, 0); err != nil || len(got) != 1 {
		t.Fatalf("public list must include the moment, got len=%d err=%v", len(got), err)
	}

	// B cannot delete A's moment: RLS hides the row, so the DELETE targets
	// nothing.
	txB := userContext(t, pool, bID)
	err = moments.Delete(database.ContextWithTx(context.Background(), txB), moment.ID)
	txB.Rollback(context.Background())
	if err != shared.ErrNotFound {
		t.Fatalf("cross-user delete must return ErrNotFound, got %v", err)
	}

	// Owner can delete.
	txA2 := userContext(t, pool, aID)
	if err := moments.Delete(database.ContextWithTx(context.Background(), txA2), moment.ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	txA2.Commit(context.Background())
}

// userContextCtx opens a user RLS tx and returns a context carrying it.
func userContextCtx(t *testing.T, pool *pgxpool.Pool, userID string) context.Context {
	t.Helper()
	tx := userContext(t, pool, userID)
	t.Cleanup(func() { tx.Rollback(context.Background()) })
	return database.ContextWithTx(context.Background(), tx)
}
