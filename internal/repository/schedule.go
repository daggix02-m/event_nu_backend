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

type EventSessionRepository struct {
	pool *pgxpool.Pool
}

func NewEventSessionRepository(pool *pgxpool.Pool) *EventSessionRepository {
	return &EventSessionRepository{pool: pool}
}

func (r *EventSessionRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const sessionColumns = `id, event_id, title, speaker, stage, starts_at, ends_at, created_at, updated_at`

func scanSession(row pgx.Row) (*domain.EventSession, error) {
	var s domain.EventSession
	err := row.Scan(&s.ID, &s.EventID, &s.Title, &s.Speaker, &s.Stage, &s.StartsAt, &s.EndsAt, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan session: %w", err)
	}
	return &s, nil
}

func (r *EventSessionRepository) Create(ctx context.Context, s *domain.EventSession) (*domain.EventSession, error) {
	return scanSession(r.q(ctx).QueryRow(ctx, `
		INSERT INTO event_sessions (event_id, title, speaker, stage, starts_at, ends_at)
		VALUES ($1, $2, nullif($3,''), nullif($4,''), $5, $6)
		RETURNING `+sessionColumns,
		s.EventID, s.Title, s.Speaker, s.Stage, s.StartsAt, s.EndsAt))
}

func (r *EventSessionRepository) GetByID(ctx context.Context, id string) (*domain.EventSession, error) {
	return scanSession(r.q(ctx).QueryRow(ctx, `
		SELECT `+sessionColumns+`
		FROM event_sessions WHERE id = $1`, id))
}

func (r *EventSessionRepository) Update(ctx context.Context, s *domain.EventSession) (*domain.EventSession, error) {
	return scanSession(r.q(ctx).QueryRow(ctx, `
		UPDATE event_sessions
		SET title = $2, speaker = nullif($3,''), stage = nullif($4,''), starts_at = $5, ends_at = $6, updated_at = now()
		WHERE id = $1
		RETURNING `+sessionColumns,
		s.ID, s.Title, s.Speaker, s.Stage, s.StartsAt, s.EndsAt))
}

func (r *EventSessionRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.q(ctx).Exec(ctx, `DELETE FROM event_sessions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return shared.ErrNotFound
	}
	return nil
}

func (r *EventSessionRepository) ListByEvent(ctx context.Context, eventID string) ([]*domain.EventSession, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+sessionColumns+`
		FROM event_sessions
		WHERE event_id = $1
		ORDER BY starts_at ASC, id`, eventID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var out []*domain.EventSession
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
