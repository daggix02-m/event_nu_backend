package repository

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestRecapAggregationAndCacheWatermarks: the recap aggregates top moments,
// public attendee count, published review summary, and session highlights; the
// cache row stays fresh until a source row bumps the activity watermark.
func TestRecapAggregationAndCacheWatermarks(t *testing.T) {
	pool := rlsTestPool(t)
	servicePool := rlsServicePool(t)
	ctx := context.Background()

	ownerID := seedRLSUserNamed(t, servicePool, "recap-owner")
	attendee := seedRLSUserNamed(t, servicePool, "recap-att")
	private := seedRLSUserNamed(t, servicePool, "recap-priv")
	reviewer2 := seedRLSUserNamed(t, servicePool, "recap-rev2")

	// One public + one private RSVP → attendee_count must be 1.
	eventID := seedPublishedEventWithRSVPs(t, servicePool, ownerID, map[string]bool{attendee: true, private: false})

	// Seed 4 moments (only the newest 3 are featured), 2 published reviews
	// (average 4.5), and 3 sessions.
	for i := 0; i < 4; i++ {
		asset := seedReadyMediaAsset(t, servicePool, attendee)
		if _, err := servicePool.Exec(ctx, `
			INSERT INTO moments (event_id, user_id, media_asset_id, caption)
			VALUES ($1, $2, $3, $4)`, eventID, attendee, asset, fmt.Sprintf("cap %d", i)); err != nil {
			t.Fatalf("seed moment: %v", err)
		}
	}
	for i, rating := range []int16{4, 5} {
		reviewer := attendee
		if i > 0 {
			reviewer = reviewer2
		}
		body := fmt.Sprintf("nice %d", rating)
		if _, err := servicePool.Exec(ctx, `
			INSERT INTO reviews (event_id, user_id, rating, body, status)
			VALUES ($1, $2, $3, $4, 'published')`, eventID, reviewer, rating, body); err != nil {
			t.Fatalf("seed review: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := servicePool.Exec(ctx, `
			INSERT INTO event_sessions (event_id, title, starts_at)
			VALUES ($1, $2, now() + make_interval(hours => $3))`, eventID, fmt.Sprintf("Session %d", i), i); err != nil {
			t.Fatalf("seed session: %v", err)
		}
	}

	recaps := NewRecapRepository(pool)
	viewer := seedRLSUserNamed(t, servicePool, "recap-viewer")
	viewerCtx := userContextCtx(t, pool, viewer)

	data, err := recaps.Aggregate(viewerCtx, eventID)
	if err != nil {
		t.Fatalf("aggregate recap: %v", err)
	}
	if data.MomentCount != 4 || len(data.TopMoments) != 3 {
		t.Fatalf("expected 4 moments / 3 featured, got count=%d top=%d", data.MomentCount, len(data.TopMoments))
	}
	if data.AttendeeCount != 1 {
		t.Fatalf("expected 1 public attendee, got %d", data.AttendeeCount)
	}
	if data.ReviewCount != 2 || data.ReviewAverage == nil || *data.ReviewAverage != 4.5 {
		t.Fatalf("expected 2 reviews avg 4.5, got count=%d avg=%v", data.ReviewCount, data.ReviewAverage)
	}
	if data.SessionCount != 3 || len(data.SessionHighlights) != 3 {
		t.Fatalf("expected 3 sessions / 3 highlights, got count=%d hi=%d", data.SessionCount, len(data.SessionHighlights))
	}

	// Cache the aggregate; the watermark must make it fresh immediately.
	if err := recaps.CachePut(viewerCtx, eventID, *data); err != nil {
		t.Fatalf("cache recap: %v", err)
	}
	cached, err := recaps.GetCached(viewerCtx, eventID)
	if err != nil || cached == nil {
		t.Fatalf("expected a cached recap, got %v err=%v", cached, err)
	}
	if cached.Data.MomentCount != 4 || cached.Data.ReviewAverage == nil || *cached.Data.ReviewAverage != 4.5 {
		t.Fatalf("cached recap must round-trip, got %+v", cached.Data)
	}

	// Fresh while the watermark is at or before generated_at.
	activity, err := recaps.ActivityAt(viewerCtx, eventID)
	if err != nil {
		t.Fatalf("activity watermark: %v", err)
	}
	if cached.GeneratedAt.Before(activity) {
		t.Fatalf("fresh cache must not predate the watermark, generated=%v activity=%v", cached.GeneratedAt, activity)
	}

	// A new moment bumps the watermark past the cached generated_at → stale.
	time.Sleep(2 * time.Millisecond)
	newAsset := seedReadyMediaAsset(t, servicePool, attendee)
	if _, err := servicePool.Exec(ctx, `
		INSERT INTO moments (event_id, user_id, media_asset_id, caption)
		VALUES ($1, $2, $3, 'brand new')`, eventID, attendee, newAsset); err != nil {
		t.Fatalf("add moment: %v", err)
	}
	activity2, err := recaps.ActivityAt(viewerCtx, eventID)
	if err != nil {
		t.Fatalf("updated watermark: %v", err)
	}
	if !activity2.After(cached.GeneratedAt) {
		t.Fatalf("new source row must make the cache stale, cached=%v activity=%v", cached.GeneratedAt, activity2)
	}
}

// TestRecapHiddenEvent: activity/aggregation for an invisible (draft) event
// must not leak — the service gates on events.GetByID first, and the definer
// helpers refuse non-published events at the SQL layer too.
func TestRecapHiddenEvent(t *testing.T) {
	pool := rlsTestPool(t)
	servicePool := rlsServicePool(t)
	ctx := context.Background()

	ownerID := seedRLSUserNamed(t, servicePool, "recap-hid-owner")
	viewer := seedRLSUserNamed(t, servicePool, "recap-hid-viewer")

	var organizerID, eventID string
	slug := fmt.Sprintf("rhorg-%d", time.Now().UnixNano())
	if err := servicePool.QueryRow(ctx, `
		INSERT INTO organizers (owner_user_id, slug, name)
		VALUES ($1, $2, $3) RETURNING id`, ownerID, slug, "Recap Hidden Org "+slug).Scan(&organizerID); err != nil {
		t.Fatalf("seed organizer: %v", err)
	}
	if err := servicePool.QueryRow(ctx, `
		INSERT INTO events (organizer_id, title, starts_at, status, moderation_status)
		VALUES ($1, $2, now() + interval '1 day', 'draft', 'clean') RETURNING id`,
		organizerID, "Recap Hidden Event "+slug).Scan(&eventID); err != nil {
		t.Fatalf("seed event: %v", err)
	}

	recaps := NewRecapRepository(pool)
	viewerCtx := userContextCtx(t, pool, viewer)

	// Draft event: CachePut and attendee-count must refuse (returns false/0).
	asset := seedReadyMediaAsset(t, servicePool, ownerID)
	if _, err := servicePool.Exec(ctx, `
		INSERT INTO moments (event_id, user_id, media_asset_id, caption)
		VALUES ($1, $2, $3, 'hidden moment')`, eventID, ownerID, asset); err != nil {
		t.Fatalf("seed moment: %v", err)
	}

	// The aggregate reads through RLS, so moments of a draft are invisible to
	// the viewer.
	data, err := recaps.Aggregate(viewerCtx, eventID)
	if err != nil {
		t.Fatalf("aggregate hidden: %v", err)
	}
	if data.MomentCount != 0 || data.SessionCount != 0 {
		t.Fatalf("draft event must aggregate to empty, got moment_count=%d session_count=%d", data.MomentCount, data.SessionCount)
	}
}
