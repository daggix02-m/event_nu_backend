package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CategoryPreferenceRepository stores the user's interest category ids as an
// ordered array with full-replace semantics. Reads/writes run under the
// request's RLS context, so a caller can only ever touch their own row.
type CategoryPreferenceRepository struct {
	pool *pgxpool.Pool
}

func NewCategoryPreferenceRepository(pool *pgxpool.Pool) *CategoryPreferenceRepository {
	return &CategoryPreferenceRepository{pool: pool}
}

func (r *CategoryPreferenceRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

// Get returns the user's category ids (empty slice when they have no row yet)
// and the last update time (nil when never set).
func (r *CategoryPreferenceRepository) Get(ctx context.Context, userID string) ([]string, *time.Time, error) {
	var (
		ids       []string
		updatedAt *time.Time
	)
	err := r.q(ctx).QueryRow(ctx, `
		SELECT category_ids, updated_at
		FROM user_category_preferences
		WHERE user_id = $1`, userID).Scan(&ids, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return []string{}, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get category preferences: %w", err)
	}
	return ids, updatedAt, nil
}

// Replace overwrites the user's category ids and returns the new updated_at.
// At least one id is enforced by the migration's cardinality CHECK.
func (r *CategoryPreferenceRepository) Replace(ctx context.Context, userID string, categoryIDs []string) (time.Time, error) {
	var updatedAt time.Time
	err := r.q(ctx).QueryRow(ctx, `
		INSERT INTO user_category_preferences (user_id, category_ids, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE
		  SET category_ids = EXCLUDED.category_ids, updated_at = now()
		RETURNING updated_at`, userID, categoryIDs).Scan(&updatedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("replace category preferences: %w", err)
	}
	return updatedAt, nil
}

// ActiveCategoryCount returns how many of the given category ids exist and are
// active. The preferences service compares it against the deduplicated input
// length to reject unknown/inactive ids.
func (r *CategoryPreferenceRepository) ActiveCategoryCount(ctx context.Context, categoryIDs []string) (int, error) {
	var n int
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*)::int
		FROM categories
		WHERE is_active = true AND id = ANY($1)`, categoryIDs).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count active categories: %w", err)
	}
	return n, nil
}
