package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AnalyticsRepository stores raw events, recomputes the daily rollup and
// exposes the aggregations the admin dashboard reads (from the rollup table
// only — raw events are worker-private).
type AnalyticsRepository struct {
	pool *pgxpool.Pool
}

func NewAnalyticsRepository(pool *pgxpool.Pool) *AnalyticsRepository {
	return &AnalyticsRepository{pool: pool}
}

func (r *AnalyticsRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

// InsertBatch inserts events with a single attribution: userID is the
// authenticated caller (empty = anonymous). The multi-row INSERT is one
// statement so the whole batch commits or rolls back atomically with the
// request transaction.
func (r *AnalyticsRepository) InsertBatch(ctx context.Context, userID string, events []domain.AnalyticsEvent) (int, error) {
	var b strings.Builder
	b.WriteString("INSERT INTO analytics_events (session_id, event_name, user_id, properties) VALUES ")
	args := make([]any, 0, len(events)*4)
	for i, e := range events {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(fmt.Sprintf("($%d, $%d, $%d, $%d)", i*4+1, i*4+2, i*4+3, i*4+4))
		args = append(args, e.SessionID, e.EventName, nullableText(userID), e.Properties)
	}

	tag, err := r.q(ctx).Exec(ctx, b.String(), args...)
	if err != nil {
		return 0, fmt.Errorf("insert analytics events: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// RollupDay recomputes the aggregates for one UTC day from the raw events and
// upserts them. Recomputing (instead of incrementing) before the ON CONFLICT
// makes the rollup idempotent: running it twice yields identical numbers, and
// late-arriving events for an already-rolled day are picked up on the next run.
func (r *AnalyticsRepository) RollupDay(ctx context.Context, day time.Time) (int, error) {
	tag, err := r.q(ctx).Exec(ctx, `
		INSERT INTO analytics_daily_feature_usage (day, event_name, event_count, unique_users)
		SELECT $1::date, e.event_name, count(*),
		       count(DISTINCT COALESCE(e.user_id::text, e.session_id::text))
		FROM analytics_events e
		WHERE (e.created_at AT TIME ZONE 'UTC')::date = $1::date
		GROUP BY e.event_name
		ON CONFLICT (day, event_name) DO UPDATE
		  SET event_count = EXCLUDED.event_count,
		      unique_users = EXCLUDED.unique_users`,
		day.UTC())
	if err != nil {
		return 0, fmt.Errorf("rollup day %s: %w", day.Format("2006-01-02"), err)
	}
	return int(tag.RowsAffected()), nil
}

// DeleteBefore removes raw events strictly older than the retention window.
// The second conjunct (UTC date < utcToday) guarantees the current UTC day is
// never touched, so a partially-rolled day can't lose events between rollups.
func (r *AnalyticsRepository) DeleteBefore(ctx context.Context, cutoff time.Time, utcToday time.Time) (int64, error) {
	tag, err := r.q(ctx).Exec(ctx, `
		DELETE FROM analytics_events
		WHERE created_at < $1
		  AND (created_at AT TIME ZONE 'UTC')::date < $2::date`,
		cutoff.UTC(), utcToday.UTC())
	if err != nil {
		return 0, fmt.Errorf("delete analytics events before %s: %w", cutoff.Format(time.RFC3339), err)
	}
	return tag.RowsAffected(), nil
}

// Usage returns the rollup rows for the last n days (most recent first).
func (r *AnalyticsRepository) Usage(ctx context.Context, days int) ([]domain.DailyUsage, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT day, event_name, event_count, unique_users
		FROM analytics_daily_feature_usage
		WHERE day >= (now() AT TIME ZONE 'UTC')::date - ($1::int - 1)
		ORDER BY day, event_name`, days)
	if err != nil {
		return nil, fmt.Errorf("list daily usage: %w", err)
	}
	defer rows.Close()

	var out []domain.DailyUsage
	for rows.Next() {
		var u domain.DailyUsage
		if err := rows.Scan(&u.Day, &u.EventName, &u.EventCount, &u.UniqueUsers); err != nil {
			return nil, fmt.Errorf("scan daily usage: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Funnel aggregates the rollup table for the given event names between
// inclusive UTC days. unique_users sums the per-day distinct counts, so it
// may over-count a user active on multiple days (documented approximation).
func (r *AnalyticsRepository) Funnel(ctx context.Context, from, to time.Time, eventNames []string) ([]domain.FunnelStep, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT event_name, sum(event_count), sum(unique_users)
		FROM analytics_daily_feature_usage
		WHERE day BETWEEN $1::date AND $2::date
		  AND event_name = ANY($3)
		GROUP BY event_name
		ORDER BY event_name`, from.UTC(), to.UTC(), eventNames)
	if err != nil {
		return nil, fmt.Errorf("funnel aggregation: %w", err)
	}
	defer rows.Close()

	var out []domain.FunnelStep
	for rows.Next() {
		var s domain.FunnelStep
		if err := rows.Scan(&s.EventName, &s.EventCount, &s.UniqueUsers); err != nil {
			return nil, fmt.Errorf("scan funnel step: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func nullableText(userID string) any {
	if userID == "" {
		return nil
	}
	return userID
}
