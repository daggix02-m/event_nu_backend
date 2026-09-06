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

type EventRepository struct {
	pool *pgxpool.Pool
}

func NewEventRepository(pool *pgxpool.Pool) *EventRepository {
	return &EventRepository{pool: pool}
}

func (r *EventRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const eventColumns = `id, organizer_id, venue_id, category_id, title, description, starts_at, ends_at,
	price_is_free, price_display, action_type, action_target, status, moderation_status, max_attendees, created_at, updated_at, deleted_at`

func scanEvent(row pgx.Row) (*domain.Event, error) {
	var e domain.Event
	err := row.Scan(&e.ID, &e.OrganizerID, &e.VenueID, &e.CategoryID, &e.Title, &e.Description,
		&e.StartsAt, &e.EndsAt, &e.PriceIsFree, &e.PriceDisplay, &e.ActionType, &e.ActionTarget,
		&e.Status, &e.ModerationStatus, &e.MaxAttendees, &e.CreatedAt, &e.UpdatedAt, &e.DeletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan event: %w", err)
	}
	return &e, nil
}

func (r *EventRepository) Create(ctx context.Context, e *domain.Event) (*domain.Event, error) {
	return scanEvent(r.q(ctx).QueryRow(ctx, `
		INSERT INTO events (organizer_id, venue_id, category_id, title, description, starts_at, ends_at,
			price_is_free, price_display, action_type, action_target, max_attendees)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING `+eventColumns,
		e.OrganizerID, e.VenueID, e.CategoryID, e.Title, e.Description, e.StartsAt, e.EndsAt,
		e.PriceIsFree, e.PriceDisplay, e.ActionType, e.ActionTarget, e.MaxAttendees))
}

// GetByID returns any event by id (owner/privileged visibility via RLS).
func (r *EventRepository) GetByID(ctx context.Context, id string) (*domain.Event, error) {
	return scanEvent(r.q(ctx).QueryRow(ctx, `
		SELECT `+eventColumns+`
		FROM events WHERE id = $1 AND deleted_at IS NULL`, id))
}

// ListVisible returns published, non-blocked events ordered by start time.
func (r *EventRepository) ListVisible(ctx context.Context) ([]*domain.Event, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+eventColumns+`
		FROM events
		WHERE status = 'published' AND moderation_status <> 'blocked' AND deleted_at IS NULL
		ORDER BY starts_at`)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	var events []*domain.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (r *EventRepository) Update(ctx context.Context, e *domain.Event) (*domain.Event, error) {
	return scanEvent(r.q(ctx).QueryRow(ctx, `
		UPDATE events SET
			venue_id = $2, category_id = $3, title = $4, description = $5, starts_at = $6, ends_at = $7,
			price_is_free = $8, price_display = $9, action_type = $10, action_target = $11, max_attendees = $12,
			updated_at = now()
		WHERE id = $1
		RETURNING `+eventColumns,
		e.ID, e.VenueID, e.CategoryID, e.Title, e.Description, e.StartsAt, e.EndsAt,
		e.PriceIsFree, e.PriceDisplay, e.ActionType, e.ActionTarget, e.MaxAttendees))
}

func (r *EventRepository) UpdateStatus(ctx context.Context, id, status string) error {
	_, err := r.q(ctx).Exec(ctx, `
		UPDATE events SET status = $2, updated_at = now() WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("update event status: %w", err)
	}
	return nil
}
