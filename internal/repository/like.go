package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LikeRepository models likes as an upsert on the composite (event_id, user_id)
// key, so Add is idempotent and a duplicate like is simply a no-op.
type LikeRepository struct {
	pool *pgxpool.Pool
}

func NewLikeRepository(pool *pgxpool.Pool) *LikeRepository {
	return &LikeRepository{pool: pool}
}

// q returns the request transaction (honoring RLS context) when active,
// otherwise the pool.
func (r *LikeRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

func (r *LikeRepository) Add(ctx context.Context, eventID, userID string) error {
	_, err := r.q(ctx).Exec(ctx, `
		INSERT INTO likes (event_id, user_id)
		VALUES ($1, $2)
		ON CONFLICT (event_id, user_id) DO NOTHING`, eventID, userID)
	if err != nil {
		return fmt.Errorf("add like: %w", err)
	}
	return nil
}

func (r *LikeRepository) Remove(ctx context.Context, eventID, userID string) error {
	_, err := r.q(ctx).Exec(ctx, `
		DELETE FROM likes
		WHERE event_id = $1 AND user_id = $2`, eventID, userID)
	if err != nil {
		return fmt.Errorf("remove like: %w", err)
	}
	return nil
}

// State returns the total like count for an event and whether the given user
// (empty string for anonymous) is among the likers. The NULLIF guard keeps the
// empty anonymous id out of uuid comparisons (” would raise a 22P02 and abort
// the request tx); bool_or then folds the resulting NULL to false.
func (r *LikeRepository) State(ctx context.Context, eventID, userID string) (int, bool, error) {
	var total int
	var liked bool
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) AS total,
		       coalesce(bool_or(user_id = NULLIF($2, '')::uuid), false) AS liked
		FROM likes
		WHERE event_id = $1`, eventID, userID).Scan(&total, &liked)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, fmt.Errorf("like state: %w", err)
	}
	return total, liked, nil
}
