package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MomentRepository struct {
	pool *pgxpool.Pool
}

func NewMomentRepository(pool *pgxpool.Pool) *MomentRepository {
	return &MomentRepository{pool: pool}
}

func (r *MomentRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const momentColumns = `id, event_id, user_id, media_asset_id, caption, created_at`

func scanMoment(row pgx.Row) (*domain.Moment, error) {
	var m domain.Moment
	err := row.Scan(&m.ID, &m.EventID, &m.UserID, &m.MediaAssetID, &m.Caption, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan moment: %w", err)
	}
	return &m, nil
}

// Create inserts a new moment. RLS with INSERT own policy enforces user_id =
// current_app_user_id(); eligibility and media ownership are checked in the
// service layer before calling this.
func (r *MomentRepository) Create(ctx context.Context, m *domain.Moment) (*domain.Moment, error) {
	return scanMoment(r.q(ctx).QueryRow(ctx, `
		INSERT INTO moments (event_id, user_id, media_asset_id, caption)
		VALUES ($1, $2, $3, nullif($4, ''))
		RETURNING `+momentColumns,
		m.EventID, m.UserID, m.MediaAssetID, m.Caption))
}

// GetByID fetches a moment under RLS (visible for published events, or owner/
// privileged). The SELECT policy on moments gates on the linked event's
// publication state.
func (r *MomentRepository) GetByID(ctx context.Context, id string) (*domain.Moment, error) {
	return scanMoment(r.q(ctx).QueryRow(ctx, `
		SELECT `+momentColumns+`
		FROM moments WHERE id = $1`, id))
}

// ListByEvent returns moments for a published event, newest first.
func (r *MomentRepository) ListByEvent(ctx context.Context, eventID string, limit, offset int) ([]*domain.Moment, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+momentColumns+`
		FROM moments
		WHERE event_id = $1
		ORDER BY created_at DESC, id
		LIMIT $2 OFFSET $3`, eventID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list moments: %w", err)
	}
	defer rows.Close()

	var out []*domain.Moment
	for rows.Next() {
		m, err := scanMoment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *MomentRepository) CountByEvent(ctx context.Context, eventID string) (int, error) {
	var total int
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) FROM moments WHERE event_id = $1`, eventID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("count moments: %w", err)
	}
	return total, nil
}

// Delete removes a moment permanently. RLS enforces own-row delete under the
// request identity; the service layer routes admin deletes to DeleteAuthorized.
func (r *MomentRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.q(ctx).Exec(ctx, `DELETE FROM moments WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete moment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return shared.ErrNotFound
	}
	return nil
}

// DeleteAuthorized removes a moment through the delete_moment SECURITY
// DEFINER helper. Callers must already be the moment owner OR an admin (the
// definer re-verifies admin from the users table against the server-set
// current_app_user_id(), so the flag can't be spoofed). Used for the admin
// moderation path where the ordinary request runs with RLS role 'user'.
func (r *MomentRepository) DeleteAuthorized(ctx context.Context, id string) error {
	var deleted bool
	err := r.q(ctx).QueryRow(ctx, `SELECT delete_moment($1)`, id).Scan(&deleted)
	if err != nil {
		return fmt.Errorf("delete moment (authorized): %w", err)
	}
	if !deleted {
		return shared.ErrNotFound
	}
	return nil
}

// HasRSVPOrTicket returns true when the user has an active RSVP (confirmed or
// attended) OR an issued ticket for the given event. Both queries touch own-
// row data only, so app_user RLS allows them.
func (r *MomentRepository) HasRSVPOrTicket(ctx context.Context, userID, eventID string) (bool, error) {
	var ok bool
	err := r.q(ctx).QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM event_rsvps
			WHERE user_id = $1 AND event_id = $2 AND status IN ('confirmed', 'attended')
		) OR EXISTS (
			SELECT 1 FROM tickets
			WHERE user_id = $1 AND event_id = $2 AND status = 'issued'
		)`, userID, eventID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check eligibility: %w", err)
	}
	return ok, nil
}

// IsValidMediaAsset verifies the media asset exists, belongs to the caller,
// is ready, and has not been soft-deleted. own-row RLS on media_assets allows
// this query under app_user.
func (r *MomentRepository) IsValidMediaAsset(ctx context.Context, userID, mediaAssetID string) (bool, error) {
	var ok bool
	err := r.q(ctx).QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM media_assets
			WHERE id = $1
			  AND uploader_id = $2
			  AND status = 'ready'
			  AND deleted_at IS NULL
		)`, mediaAssetID, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("validate media asset: %w", err)
	}
	return ok, nil
}

// ListAttendees calls the SECURITY DEFINER list_event_attendees() function.
// The definer verifies the event is published and returns public_rsvp attendees.
func (r *MomentRepository) ListAttendees(ctx context.Context, eventID string, limit, offset int) ([]*domain.MomentAttendee, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT * FROM list_event_attendees($1, $2, $3)`, eventID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list attendees: %w", err)
	}
	defer rows.Close()

	var out []*domain.MomentAttendee
	for rows.Next() {
		var a domain.MomentAttendee
		var avatar *string
		if err := rows.Scan(&a.UserID, &a.DisplayName, &avatar, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan attendee: %w", err)
		}
		if avatar != nil {
			a.AvatarURL = *avatar
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// CountAttendees calls the SECURITY DEFINER count_event_attendees() function.
func (r *MomentRepository) CountAttendees(ctx context.Context, eventID string) (int, error) {
	var total int
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count_event_attendees($1)`, eventID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("count attendees: %w", err)
	}
	return total, nil
}
