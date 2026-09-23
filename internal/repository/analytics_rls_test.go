package repository

import (
	"context"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testSession = "550e8400-e29b-41d4-a716-446655440000"

// TestRLSAnalyticsInsertAttributesCaller proves:
//  1. under the caller's RLS identity, inserting events attributed to THEM is
//     allowed (and user_id is preserved);
//  2. inserting events attributed to ANOTHER user is rejected by the INSERT
//     policy (attribution cannot be spoofed);
//  3. anonymous batches (NULL user_id) are allowed.
func TestRLSAnalyticsInsertAttributesCaller(t *testing.T) {
	pool := rlsTestPool(t)
	servicePool := rlsServicePool(t)
	aID := seedRLSUser(t, servicePool)
	bID := seedRLSUser(t, servicePool)

	repo := NewAnalyticsRepository(pool)

	// Purge rows from earlier runs so counts are scoped to this run.
	if _, err := servicePool.Exec(context.Background(),
		"DELETE FROM analytics_events WHERE session_id = $1", testSession); err != nil {
		t.Fatalf("purge session: %v", err)
	}

	// Authenticated as A: insert with user_id = A succeeds.
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
	if _, err := repo.InsertBatch(rctx, *aID, []domain.AnalyticsEvent{
		{SessionID: testSession, EventName: domain.EventScreenViewed, Properties: map[string]any{}},
	}); err != nil {
		t.Fatalf("self-attributed insert should succeed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// The row exists and carries A's user_id (checked under service role).
	var n int
	var attributed *string
	err = servicePool.QueryRow(context.Background(),
		"SELECT count(*), min(user_id::text) FROM analytics_events WHERE session_id = $1",
		testSession).Scan(&n, &attributed)
	if err != nil {
		t.Fatalf("verify row: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 row, got %d", n)
	}
	if attributed == nil || *attributed != *aID {
		t.Fatalf("expected attribution to %s, got %v", *aID, attributed)
	}

	// Authenticated as A: attempting to attribute to B is rejected by RLS.
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx 2: %v", err)
	}
	defer tx.Rollback(ctx)
	rctx = database.ContextWithTx(ctx, tx)
	if err := database.SetRLSContextTx(rctx, tx, *aID, "user"); err != nil {
		t.Fatalf("set rls context 2: %v", err)
	}
	if _, err := repo.InsertBatch(rctx, *bID, []domain.AnalyticsEvent{
		{SessionID: testSession, EventName: domain.EventEventDetailViewed, Properties: map[string]any{}},
	}); err == nil {
		t.Fatal("attributing events to another user must be blocked by RLS")
	}

	// Anonymous insert (userID "") is allowed and stores NULL.
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx 3: %v", err)
	}
	defer tx.Rollback(ctx)
	rctx = database.ContextWithTx(ctx, tx)
	if err := database.SetRLSContextTx(rctx, tx, *aID, "user"); err != nil {
		t.Fatalf("set rls context 3: %v", err)
	}
	if _, err := repo.InsertBatch(rctx, "", []domain.AnalyticsEvent{
		{SessionID: testSession, EventName: domain.EventEventListViewed, Properties: map[string]any{}},
	}); err != nil {
		t.Fatalf("anonymous insert should succeed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit 3: %v", err)
	}

	var anonUserID *string
	err = servicePool.QueryRow(context.Background(),
		"SELECT user_id::text FROM analytics_events WHERE event_name = 'event_list_viewed' AND session_id = $1",
		testSession).Scan(&anonUserID)
	if err != nil {
		t.Fatalf("verify anonymous row: %v", err)
	}
	if anonUserID != nil {
		t.Fatalf("anonymous event must have NULL user_id, got %v", *anonUserID)
	}
}

// TestRLSBloocksRawEventReadsByNonPrivileged proves app users (even their own)
// cannot read the raw event log — SELECT is privileged-only.
func TestRLSBloocksRawEventReadsByNonPrivileged(t *testing.T) {
	pool := rlsTestPool(t)
	servicePool := rlsServicePool(t)
	aID := seedRLSUser(t, servicePool)

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
	if _, err := tx.Exec(ctx, "SELECT count(*) FROM analytics_events"); err != nil {
		t.Fatalf("select on analytics_events should not error under RLS: %v", err)
	}
	var rawCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM analytics_events").Scan(&rawCount); err != nil {
		t.Fatalf("count raw: %v", err)
	}
	if rawCount != 0 {
		t.Fatalf("non-privileged user must see ZERO raw events (RLS), got %d", rawCount)
	}

	var dailyCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM analytics_daily_feature_usage").Scan(&dailyCount); err != nil {
		t.Fatalf("count daily: %v", err)
	}
	if dailyCount != 0 {
		t.Fatalf("non-privileged user must see ZERO rollup rows, got %d", dailyCount)
	}
}

// purgeDay removes raw events + rollup rows for one UTC day so integration
// assertions are scoped to exactly what a test seeds (the shared dev DB
// accumulates rows across runs).
func purgeDay(t *testing.T, servicePool *pgxpool.Pool, day time.Time) {
	t.Helper()
	ctx := context.Background()
	for _, sql := range []string{
		`DELETE FROM analytics_daily_feature_usage WHERE day = $1::date`,
		`DELETE FROM analytics_events WHERE (created_at AT TIME ZONE 'UTC')::date = $1::date`,
	} {
		if _, err := servicePool.Exec(ctx, sql, day); err != nil {
			t.Fatalf("purge %s: %v", day.Format("2006-01-02"), err)
		}
	}
}

func TestAnalyticsRollupIdempotent(t *testing.T) {
	servicePool := rlsServicePool(t)

	day := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	purgeDay(t, servicePool, day)
	// The rollup (raw read) runs under the service-role pool, like production.
	repo := NewAnalyticsRepository(servicePool)

	// Seed raw events (service role) for one fixed UTC day.
	sink := servicePool
	for _, s := range []struct {
		session string
		name    string
	}{
		{"11111111-1111-4111-8111-111111111111", domain.EventEventListViewed},
		{"11111111-1111-4111-8111-111111111111", domain.EventEventDetailViewed},
		{"22222222-2222-4222-8222-222222222222", domain.EventEventListViewed},
		{"22222222-2222-4222-8222-222222222222", domain.EventEventListViewed},
	} {
		_, err := sink.Exec(context.Background(),
			`INSERT INTO analytics_events (session_id, event_name, user_id, properties, created_at)
			 VALUES ($1, $2, NULL, '{}'::jsonb, $3)`,
			s.session, s.name, day)
		if err != nil {
			t.Fatalf("seed event: %v", err)
		}
	}

	// Run the rollup twice; both runs must agree exactly.
	for run := 1; run <= 2; run++ {
		rows, err := repo.RollupDay(context.Background(), day)
		if err != nil {
			t.Fatalf("rollup run %d: %v", run, err)
		}
		// On the second run, updating the same 2 rows also reports 2.
		if rows != 2 {
			t.Fatalf("rollup run %d: expected 2 affected rows, got %d", run, rows)
		}
	}

	var listCount, listUnique int64
	err := servicePool.QueryRow(context.Background(),
		`SELECT event_count, unique_users FROM analytics_daily_feature_usage
		 WHERE day = $1 AND event_name = $2`, day, domain.EventEventListViewed).
		Scan(&listCount, &listUnique)
	if err != nil {
		t.Fatalf("read rollup: %v", err)
	}
	if listCount != 3 {
		t.Fatalf("expected 3 event_list_viewed, got %d", listCount)
	}
	if listUnique != 2 {
		t.Fatalf("expected 2 unique sessions for event_list_viewed, got %d", listUnique)
	}
}

// TestAnalyticsRetentionHappyPathAndCurrentDayGuard proves retention deletes
// old days but never the current UTC day. All events use a session id unique to
// this test so other tests' rows never skew the counts.
func TestAnalyticsRetentionHappyPathAndCurrentDayGuard(t *testing.T) {
	servicePool := rlsServicePool(t)
	repo := NewAnalyticsRepository(servicePool)

	session := "50000000-0000-4000-8000-000000000001"
	// Purge any leftover rows from earlier runs of this test.
	if _, err := servicePool.Exec(context.Background(),
		"DELETE FROM analytics_events WHERE session_id = $1", session); err != nil {
		t.Fatalf("purge session: %v", err)
	}
	oldDay := time.Now().UTC().Add(-100 * 24 * time.Hour)
	today := time.Now().UTC()
	for _, d := range []time.Time{oldDay, today} {
		_, err := servicePool.Exec(context.Background(),
			`INSERT INTO analytics_events (session_id, event_name, user_id, properties, created_at)
			 VALUES ($1, $2, NULL, '{}'::jsonb, $3)`,
			session, domain.EventScreenViewed, d)
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	utcToday := today.Truncate(24 * time.Hour)
	cutoff := today.Add(-90 * 24 * time.Hour)
	deleted, err := repo.DeleteBefore(context.Background(), cutoff, utcToday)
	if err != nil {
		t.Fatalf("delete before: %v", err)
	}
	// DeleteBefore is global; assert OUR old row was removed and today's kept.
	if deleted < 1 {
		t.Fatalf("expected at least 1 deleted, got %d", deleted)
	}

	var remaining int
	err = servicePool.QueryRow(context.Background(),
		"SELECT count(*) FROM analytics_events WHERE session_id = $1", session).Scan(&remaining)
	if err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("expected exactly the current-day event to remain, got %d", remaining)
	}
	var todayKept int
	err = servicePool.QueryRow(context.Background(),
		"SELECT count(*) FROM analytics_events WHERE session_id = $1 AND (created_at AT TIME ZONE 'UTC')::date = $2::date",
		session, utcToday).Scan(&todayKept)
	if err != nil {
		t.Fatalf("count today kept: %v", err)
	}
	if todayKept != 1 {
		t.Fatalf("the current UTC day's event must never be deleted, got %d kept", todayKept)
	}
}

// TestAnalyticsAdminReadsRollup proves the admin role can read the rollup via
// Funnel and that aggregated numbers are correct.
func TestAnalyticsAdminReadsRollup(t *testing.T) {
	pool := rlsTestPool(t)
	servicePool := rlsServicePool(t)

	day := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	purgeDay(t, servicePool, day)
	for _, d := range []struct {
		event string
		sess  string
	}{
		{domain.EventOnboardingStepViewed, "11111111-1111-4111-8111-111111111111"},
		{domain.EventOnboardingStepViewed, "22222222-2222-4222-8222-222222222222"},
		{domain.EventOnboardingCompleted, "11111111-1111-4111-8111-111111111111"},
		{domain.EventOnboardingSkipped, "33333333-3333-4333-8333-333333333333"},
	} {
		_, err := servicePool.Exec(context.Background(),
			`INSERT INTO analytics_events (session_id, event_name, user_id, properties, created_at)
			 VALUES ($1, $2, NULL, '{}'::jsonb, $3)`,
			d.sess, d.event, day)
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if _, err := NewAnalyticsRepository(servicePool).RollupDay(context.Background(), day); err != nil {
		t.Fatalf("rollup: %v", err)
	}

	// Admin tx context.
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(ctx)
	rctx := database.ContextWithTx(ctx, tx)
	if err := database.SetRLSContextTx(rctx, tx, "00000000-0000-4000-8000-0000000000ad", "admin"); err != nil {
		t.Fatalf("set rls context: %v", err)
	}
	repo := NewAnalyticsRepository(pool)

	steps, err := repo.Funnel(rctx, day, day, domain.OnboardingFunnel)
	if err != nil {
		t.Fatalf("funnel: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("expected 3 funnel steps, got %d", len(steps))
	}
	byName := map[string]domain.FunnelStep{}
	for _, s := range steps {
		byName[s.EventName] = s
	}
	if byName[domain.EventOnboardingStepViewed].EventCount != 2 {
		t.Fatalf("expected 2 step_viewed, got %d", byName[domain.EventOnboardingStepViewed].EventCount)
	}
	if byName[domain.EventOnboardingSkipped].EventCount != 1 {
		t.Fatalf("expected 1 skipped, got %d", byName[domain.EventOnboardingSkipped].EventCount)
	}
	if byName[domain.EventOnboardingCompleted].UniqueUsers != 1 {
		t.Fatalf("expected 1 unique user for completed, got %d", byName[domain.EventOnboardingCompleted].UniqueUsers)
	}

	usage, err := repo.Usage(rctx, 90)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if len(usage) == 0 {
		t.Fatal("expected at least the seeded day in the usage rollup")
	}
}
