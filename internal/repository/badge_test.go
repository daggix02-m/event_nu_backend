package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
)

// TestBadgeMilestoneGuardAndExactlyOnce: award_badge() only awards when the
// milestone rows genuinely exist, never twice (unique constraint), and badges
// are only listable under the earning user's identity.
func TestBadgeMilestoneGuardAndExactlyOnce(t *testing.T) {
	pool := rlsTestPool(t)
	service := rlsServicePool(t)

	aID := seedRLSUserNamed(t, service, "badge-a")
	bID := seedRLSUserNamed(t, service, "badge-b")
	badges := NewBadgeRepository(pool)

	// Milestone guard: no RSVP yet, unknown types never award.
	if ok, err := badges.Award(context.Background(), aID, domain.BadgeFirstRSVP, nil); err != nil || ok {
		t.Fatalf("first_rsvp without an RSVP must not award, got ok=%v err=%v", ok, err)
	}
	if ok, err := badges.Award(context.Background(), aID, "made_up_type", nil); err != nil || ok {
		t.Fatalf("unknown badge type must not award, got ok=%v err=%v", ok, err)
	}
	if ok, err := badges.Award(context.Background(), aID, domain.BadgeFirstTicket, nil); err != nil || ok {
		t.Fatalf("first_ticket without a paid order must not award, got ok=%v err=%v", ok, err)
	}

	// Seed a confirmed RSVP so the milestone is real (aID is both the organizer
	// owner and the RSVP holder). First award succeeds, the retry is a no-op.
	seedPublishedEventWithRSVPs(t, service, aID, map[string]bool{aID: true})
	if ok, err := badges.Award(context.Background(), aID, domain.BadgeFirstRSVP, map[string]any{"event_id": "x"}); err != nil || !ok {
		t.Fatalf("first_rsvp with an RSVP must award, got ok=%v err=%v", ok, err)
	}
	if ok, err := badges.Award(context.Background(), aID, domain.BadgeFirstRSVP, nil); err != nil || ok {
		t.Fatalf("duplicate award must be a no-op, got ok=%v err=%v", ok, err)
	}

	// Owner lists their badge; another user sees none of it.
	owned, err := badges.ListByUser(userContextCtx(t, pool, aID), aID)
	if err != nil || len(owned) != 1 || owned[0].BadgeType != domain.BadgeFirstRSVP {
		t.Fatalf("owner must list exactly one first_rsvp badge, got len=%d err=%v", len(owned), err)
	}
	other, err := badges.ListByUser(userContextCtx(t, pool, bID), bID)
	if err != nil || len(other) != 0 {
		t.Fatalf("non-owner must see no badges, got len=%d err=%v", len(other), err)
	}
}

// TestBadgeFirstAttendedViaReviewProxy: a started event + confirmed RSVP (the
// review attendance proxy) earns first_attended exactly once.
func TestBadgeFirstAttendedViaReviewProxy(t *testing.T) {
	pool := rlsTestPool(t)
	service := rlsServicePool(t)

	aID := seedRLSUserNamed(t, service, "badge-att-a")
	badges := NewBadgeRepository(pool)

	ctx := context.Background()
	slug := fmt.Sprintf("battorg-%d", time.Now().UnixNano())
	var organizerID, eventID string
	if err := service.QueryRow(ctx, `
		INSERT INTO organizers (owner_user_id, slug, name)
		VALUES ($1, $2, $3) RETURNING id`, aID, slug, "Badge Att Org "+slug).Scan(&organizerID); err != nil {
		t.Fatalf("seed organizer: %v", err)
	}
	if err := service.QueryRow(ctx, `
		INSERT INTO events (organizer_id, title, starts_at, status, moderation_status)
		VALUES ($1, $2, now() - interval '1 hour', 'published', 'clean') RETURNING id`,
		organizerID, "Badge Att Event "+slug).Scan(&eventID); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := service.Exec(ctx, `
		INSERT INTO event_rsvps (event_id, user_id, status, public_rsvp)
		VALUES ($1, $2, 'confirmed', true)`, eventID, aID); err != nil {
		t.Fatalf("seed rsvp: %v", err)
	}

	if ok, err := badges.Award(ctx, aID, domain.BadgeFirstAttended, nil); err != nil || !ok {
		t.Fatalf("attended milestone must award, got ok=%v err=%v", ok, err)
	}
	if ok, err := badges.Award(ctx, aID, domain.BadgeFirstAttended, nil); err != nil || ok {
		t.Fatalf("duplicate attended award must be a no-op, got ok=%v err=%v", ok, err)
	}
}

// TestBadgeFirstAttendedNotBeforeEvent starts: an RSVP alone (event hasn't
// started) must not satisfy the attended milestone.
func TestBadgeFirstAttendedNotBeforeEvent(t *testing.T) {
	pool := rlsTestPool(t)
	service := rlsServicePool(t)

	aID := seedRLSUserNamed(t, service, "badge-not-yet")
	badges := NewBadgeRepository(pool)

	// seedPublishedEventWithRSVPs creates an event starting tomorrow.
	seedPublishedEventWithRSVPs(t, service, aID, map[string]bool{aID: true})
	if ok, err := badges.Award(context.Background(), aID, domain.BadgeFirstAttended, nil); err != nil || ok {
		t.Fatalf("attended milestone must not award before the event starts, got ok=%v err=%v", ok, err)
	}
}
