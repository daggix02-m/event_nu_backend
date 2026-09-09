package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FollowRepository models organizer follows. Reads are public (RLS) so follower
// counts aggregate for everyone; writes are own-row only.
type FollowRepository struct {
	pool *pgxpool.Pool
}

func NewFollowRepository(pool *pgxpool.Pool) *FollowRepository {
	return &FollowRepository{pool: pool}
}

func (r *FollowRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

// Follow records a follow and reports whether the row was newly inserted
// (false = the follow already existed).
func (r *FollowRepository) Follow(ctx context.Context, userID, organizerID string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
		INSERT INTO organizer_follows (user_id, organizer_id)
		VALUES ($1, $2)
		ON CONFLICT (user_id, organizer_id) DO NOTHING`, userID, organizerID)
	if err != nil {
		return false, fmt.Errorf("follow organizer: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (r *FollowRepository) Unfollow(ctx context.Context, userID, organizerID string) error {
	_, err := r.q(ctx).Exec(ctx, `
		DELETE FROM organizer_follows
		WHERE user_id = $1 AND organizer_id = $2`, userID, organizerID)
	if err != nil {
		return fmt.Errorf("unfollow organizer: %w", err)
	}
	return nil
}

// State returns follower count and whether the given user (empty for
// anonymous) follows. NULLIF keeps the empty anonymous id out of uuid casts.
func (r *FollowRepository) State(ctx context.Context, organizerID, userID string) (int, bool, error) {
	var total int
	var followed bool
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) AS total,
		       coalesce(bool_or(user_id = NULLIF($2, '')::uuid), false) AS followed
		FROM organizer_follows
		WHERE organizer_id = $1`, organizerID, userID).Scan(&total, &followed)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, fmt.Errorf("follow state: %w", err)
	}
	return total, followed, nil
}
