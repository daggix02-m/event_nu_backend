package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RsvpRepository manages event RSVPs. Writes go through the SECURITY DEFINER
// attempt_event_rsvp helper (atomic capacity + duplicate detection); reads are
// own-row scoped by RLS.
type RsvpRepository struct {
	pool *pgxpool.Pool
}

func NewRsvpRepository(pool *pgxpool.Pool) *RsvpRepository {
	return &RsvpRepository{pool: pool}
}

func (r *RsvpRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const rsvpColumns = "id, event_id, user_id, status, quantity, public_rsvp, created_at, updated_at"

func scanRsvp(row pgx.Row) (*domain.Rsvp, error) {
	var r domain.Rsvp
	err := row.Scan(&r.ID, &r.EventID, &r.UserID, &r.Status, &r.Quantity, &r.PublicRsvp, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan rsvp: %w", err)
	}
	return &r, nil
}

// AttemptRSVP calls the atomic capacity helper. Returns ("ok", rsvp, nil) on
// success and a machine-readable reason otherwise.
func (r *RsvpRepository) AttemptRSVP(ctx context.Context, eventID, userID string, publicRsvp bool) (string, *domain.Rsvp, error) {
	tx := r.q(ctx)
	var status string
	row := tx.QueryRow(ctx, `SELECT attempt_event_rsvp($1, $2, $3)`, eventID, userID, publicRsvp)
	if err := row.Scan(&status); err != nil {
		return "", nil, fmt.Errorf("attempt_event_rsvp: %w", err)
	}
	if status != "ok" {
		return status, nil, nil
	}
	rsvp, err := scanRsvp(tx.QueryRow(ctx, `
		SELECT `+rsvpColumns+`
		FROM event_rsvps WHERE event_id = $1 AND user_id = $2`, eventID, userID))
	if err != nil {
		return "", nil, err
	}
	return status, rsvp, nil
}

// CancelRSVP removes the caller's RSVP (releases capacity). Idempotent: a
// missing RSVP is not an error.
func (r *RsvpRepository) CancelRSVP(ctx context.Context, userID, eventID string) error {
	_, err := r.q(ctx).Exec(ctx, `
		DELETE FROM event_rsvps
		WHERE user_id = $1 AND event_id = $2`, userID, eventID)
	if err != nil {
		return fmt.Errorf("cancel rsvp: %w", err)
	}
	return nil
}

func (r *RsvpRepository) GetState(ctx context.Context, userID, eventID string) (*domain.Rsvp, error) {
	return scanRsvp(r.q(ctx).QueryRow(ctx, `
		SELECT `+rsvpColumns+`
		FROM event_rsvps WHERE user_id = $1 AND event_id = $2`, userID, eventID))
}

// ListByUser returns the caller's RSVPs joined onto event summaries (nullable
// when the event is no longer publicly visible under RLS).
func (r *RsvpRepository) ListByUser(ctx context.Context, userID string, limit, offset int) ([]*domain.RsvpWithEvent, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT e.id, e.user_id, e.event_id, e.status, e.quantity, e.public_rsvp, e.created_at, e.updated_at,
		       COALESCE(ev.title, ''),
		       COALESCE(ev.starts_at, to_timestamp(0)) AS event_starts_at
		FROM event_rsvps e
		LEFT JOIN events ev ON ev.id = e.event_id
		WHERE e.user_id = $1
		ORDER BY e.created_at DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list rsvps: %w", err)
	}
	defer rows.Close()

	var out []*domain.RsvpWithEvent
	for rows.Next() {
		var e domain.RsvpWithEvent
		if err := rows.Scan(&e.ID, &e.UserID, &e.EventID, &e.Status, &e.Quantity, &e.PublicRsvp,
			&e.CreatedAt, &e.UpdatedAt, &e.EventTitle, &e.EventTime); err != nil {
			return nil, fmt.Errorf("scan rsvp row: %w", err)
		}
		if e.EventTime != nil && e.EventTime.Equal(time.Unix(0, 0)) {
			e.EventTime = nil
		}
		out = append(out, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rsvps: %w", err)
	}
	return out, nil
}

func (r *RsvpRepository) CountByUser(ctx context.Context, userID string) (int, error) {
	var n int
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) FROM event_rsvps WHERE user_id = $1`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count rsvps: %w", err)
	}
	return n, nil
}

// ListUserIDsByEvent returns (up to limit) user ids with an active RSVP to the
// event, for fan-out notifications. The count applied guards against one event
// blowing up fan-out; it runs under the service role so every RSVP is visible.
func (r *RsvpRepository) ListUserIDsByEvent(ctx context.Context, eventID string, limit int) ([]string, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT user_id FROM event_rsvps
		WHERE event_id = $1 AND status IN ('confirmed', 'attended')
		ORDER BY created_at, id
		LIMIT $2`, eventID, limit)
	if err != nil {
		return nil, fmt.Errorf("list rsvp user ids: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan rsvp user id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CountAttended returns the number of active RSVPs for an event for a given
// user (attendee-list eligibility). Own-row RLS exposes only the caller's rows.
func (r *RsvpRepository) CountAttended(ctx context.Context, userID, eventID string) (int, error) {
	var n int
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) FROM event_rsvps
		WHERE user_id = $1 AND event_id = $2 AND status IN ('confirmed', 'attended')`,
		userID, eventID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count attended rsvps: %w", err)
	}
	return n, nil
}
