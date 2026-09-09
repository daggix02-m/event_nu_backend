package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ShareRepository records share-analytics rows. The shares_dedup_key unique
// constraint makes a repeat share of the same (user, event, channel, ref) a
// no-op, matching the idempotent-tap guarantee of the toggle endpoints.
type ShareRepository struct {
	pool *pgxpool.Pool
}

func NewShareRepository(pool *pgxpool.Pool) *ShareRepository {
	return &ShareRepository{pool: pool}
}

func (r *ShareRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

func (r *ShareRepository) Record(ctx context.Context, s *domain.Share) error {
	err := r.q(ctx).QueryRow(ctx, `
		INSERT INTO shares (user_id, event_id, channel, ref)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, event_id, channel, ref) DO NOTHING
		RETURNING id, created_at`,
		s.UserID, s.EventID, s.Channel, s.Ref).Scan(&s.ID, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// Deduplicated share of the same (user, event, channel, ref): the row
		// already exists, which is the successful idempotent outcome.
		return nil
	}
	if err != nil {
		return fmt.Errorf("record share: %w", err)
	}
	return nil
}
