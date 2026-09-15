package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RecapRepository backs the event recap dashboard. The cache lives in
// event_recaps and is written through the SECURITY DEFINER cache_event_recap()
// helper (an anonymous app_user could otherwise poison the cache). Aggregation
// runs across moments/reviews/sessions/RSVPs — tables whose own RLS would hide
// other users' rows from a public reader, so the piece that needs every row
// (activity watermark) is delegated to the event_recap_activity() definer,
// while the public-facing summaries read through normal RLS.
type RecapRepository struct {
	pool *pgxpool.Pool
}

func NewRecapRepository(pool *pgxpool.Pool) *RecapRepository {
	return &RecapRepository{pool: pool}
}

func (r *RecapRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

// ActivityAt returns the newest source timestamp (moments/reviews/sessions/
// RSVPs) for an event, or the Unix epoch when nothing exists yet. A cached
// recap is fresh while its generated_at is at or after this watermark.
func (r *RecapRepository) ActivityAt(ctx context.Context, eventID string) (time.Time, error) {
	var at time.Time
	err := r.q(ctx).QueryRow(ctx, `SELECT event_recap_activity($1)`, eventID).Scan(&at)
	if err != nil {
		return time.Time{}, fmt.Errorf("recap activity: %w", err)
	}
	return at, nil
}

// GetCached returns the cached recap for an event, or (nil, nil) when none
// exists yet.
func (r *RecapRepository) GetCached(ctx context.Context, eventID string) (*domain.EventRecap, error) {
	var raw []byte
	var generatedAt time.Time
	err := r.q(ctx).QueryRow(ctx, `
		SELECT data, generated_at FROM event_recaps WHERE event_id = $1`, eventID).
		Scan(&raw, &generatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get cached recap: %w", err)
	}
	var data domain.RecapData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("decode cached recap: %w", err)
	}
	return &domain.EventRecap{EventID: eventID, Data: data, GeneratedAt: generatedAt}, nil
}

// CachePut stores (or refreshes) the cached recap for a published event via the
// definer helper.
func (r *RecapRepository) CachePut(ctx context.Context, eventID string, data domain.RecapData) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode recap: %w", err)
	}
	var ok bool
	err = r.q(ctx).QueryRow(ctx, `SELECT cache_event_recap($1, $2)`, eventID, raw).Scan(&ok)
	if err != nil {
		return fmt.Errorf("cache recap: %w", err)
	}
	return nil
}

// Aggregate computes a fresh recap for a published event. Runs under the
// caller's RLS identity; moment/review/session reads are public for published
// events, and the attendee count uses the count_event_attendees() definer.
func (r *RecapRepository) Aggregate(ctx context.Context, eventID string) (*domain.RecapData, error) {
	data := &domain.RecapData{TopMoments: []domain.RecapMoment{}, SessionHighlights: []domain.RecapSession{}}

	if err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) FROM moments WHERE event_id = $1`, eventID).Scan(&data.MomentCount); err != nil {
		return nil, fmt.Errorf("count recap moments: %w", err)
	}
	rows, err := r.q(ctx).Query(ctx, `
		SELECT id, COALESCE(caption, ''), media_asset_id, created_at
		FROM moments
		WHERE event_id = $1
		ORDER BY created_at DESC, id
		LIMIT 3`, eventID)
	if err != nil {
		return nil, fmt.Errorf("list recap moments: %w", err)
	}
	for rows.Next() {
		var m domain.RecapMoment
		if err := rows.Scan(&m.ID, &m.Caption, &m.MediaAssetID, &m.CreatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan recap moment: %w", err)
		}
		data.TopMoments = append(data.TopMoments, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recap moments: %w", err)
	}

	if err := r.q(ctx).QueryRow(ctx, `SELECT count_event_attendees($1)`, eventID).Scan(&data.AttendeeCount); err != nil {
		return nil, fmt.Errorf("count recap attendees: %w", err)
	}

	var avg float64
	if err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*), COALESCE(avg(rating)::float8, 0)
		FROM reviews
		WHERE event_id = $1 AND status = 'published' AND deleted_at IS NULL`, eventID).
		Scan(&data.ReviewCount, &avg); err != nil {
		return nil, fmt.Errorf("aggregate recap reviews: %w", err)
	}
	if data.ReviewCount > 0 {
		data.ReviewAverage = &avg
	}

	if err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) FROM event_sessions WHERE event_id = $1`, eventID).Scan(&data.SessionCount); err != nil {
		return nil, fmt.Errorf("count recap sessions: %w", err)
	}
	sessRows, err := r.q(ctx).Query(ctx, `
		SELECT id, title, speaker, stage, starts_at
		FROM event_sessions
		WHERE event_id = $1
		ORDER BY starts_at ASC, id
		LIMIT 3`, eventID)
	if err != nil {
		return nil, fmt.Errorf("list recap sessions: %w", err)
	}
	for sessRows.Next() {
		var s domain.RecapSession
		if err := sessRows.Scan(&s.ID, &s.Title, &s.Speaker, &s.Stage, &s.StartsAt); err != nil {
			sessRows.Close()
			return nil, fmt.Errorf("scan recap session: %w", err)
		}
		data.SessionHighlights = append(data.SessionHighlights, s)
	}
	sessRows.Close()
	if err := sessRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recap sessions: %w", err)
	}

	return data, nil
}
