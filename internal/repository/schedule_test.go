package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedOrganizerEventWithSession seeds an organizer, a published event, and a
// session owned by that event under the service pool (bypassing RLS).
func seedOrganizerEventWithSession(t *testing.T, service *pgxpool.Pool, ownerUserID string, title string) (eventID, sessionID string) {
	t.Helper()
	ctx := context.Background()
	var orgID, evID string
	slug := fmt.Sprintf("sessorg-%d", time.Now().UnixNano())
	if err := service.QueryRow(ctx, `
		INSERT INTO organizers (owner_user_id, slug, name) VALUES ($1, $2, $3) RETURNING id`,
		ownerUserID, slug, "Schedule Org "+slug).Scan(&orgID); err != nil {
		t.Fatalf("seed organizer: %v", err)
	}
	if err := service.QueryRow(ctx, `
		INSERT INTO events (organizer_id, title, starts_at, status, moderation_status)
		VALUES ($1, $2, now() + interval '1 day', 'published', 'clean') RETURNING id`,
		orgID, "Schedule Event "+slug).Scan(&evID); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	return evID, ""
}

func TestScheduleRepositoryCRUD(t *testing.T) {
	pool := rlsTestPool(t)
	service := rlsServicePool(t)

	ownerID := seedRLSUserNamed(t, service, "sess-own")
	eventID, _ := seedOrganizerEventWithSession(t, service, ownerID, "test")

	repo := NewEventSessionRepository(pool)

	tx := userContext(t, pool, ownerID)
	defer tx.Rollback(context.Background())
	ctx := database.ContextWithTx(context.Background(), tx)

	sess := &domain.EventSession{
		EventID:  eventID,
		Title:    "Keynote",
		StartsAt: time.Now().Add(time.Hour),
	}
	created, err := repo.Create(ctx, sess)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Title != "Keynote" || created.EventID != eventID {
		t.Fatalf("unexpected: %+v", created)
	}

	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("get mismatch: %v vs %v", got.ID, created.ID)
	}

	got.Title = "Updated Keynote"
	updated, err := repo.Update(ctx, got)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Title != "Updated Keynote" {
		t.Fatalf("update title: %s", updated.Title)
	}

	sessions, err := repo.ListByEvent(ctx, eventID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1, got %d", len(sessions))
	}

	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	sessions, err = repo.ListByEvent(ctx, eventID)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected 0, got %d", len(sessions))
	}
}

func TestScheduleRLSBlockedForOtherUser(t *testing.T) {
	pool := rlsTestPool(t)
	service := rlsServicePool(t)

	ownerID := seedRLSUserNamed(t, service, "sess-rls-own")
	otherID := seedRLSUserNamed(t, service, "sess-rls-oth")
	eventID, _ := seedOrganizerEventWithSession(t, service, ownerID, "rls")

	repo := NewEventSessionRepository(pool)

	// Seed a session directly on the service pool so it is committed and
	// visible to other connections.
	var seededID string
	if err := service.QueryRow(context.Background(), `
		INSERT INTO event_sessions (event_id, title, starts_at)
		VALUES ($1, $2, now() + interval '1 hour') RETURNING id`, eventID, "Owner Session").Scan(&seededID); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	// Other user sees the session (published event), but cannot create one.
	otherTx := userContext(t, pool, otherID)
	defer otherTx.Rollback(context.Background())
	otherCtx := database.ContextWithTx(context.Background(), otherTx)

	list, err := repo.ListByEvent(otherCtx, eventID)
	if err != nil {
		t.Fatalf("other list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("other should see session: %d", len(list))
	}

	_, err = repo.Create(otherCtx, &domain.EventSession{
		EventID:  eventID,
		Title:    "Other Session",
		StartsAt: time.Now().Add(2 * time.Hour),
	})
	if err == nil {
		t.Fatal("other user should not be able to create a session")
	}
	_ = seededID
}
