package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BadgeRepository manages milestone badges. Writes go through the SECURITY
// DEFINER award_badge() helper (RLS forbids an app_user from inserting badge
// rows), which also enforces milestone eligibility and exactly-once awards.
type BadgeRepository struct {
	pool *pgxpool.Pool
}

func NewBadgeRepository(pool *pgxpool.Pool) *BadgeRepository {
	return &BadgeRepository{pool: pool}
}

func (r *BadgeRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

// Award attempts to award a badge to a user. Returns true only when the badge
// was newly earned; false when the milestone isn't met yet or it was already
// awarded (the unique constraint makes repeats a no-op).
func (r *BadgeRepository) Award(ctx context.Context, userID, badgeType string, metadata map[string]any) (bool, error) {
	raw, _ := json.Marshal(metadata)
	var awarded bool
	err := r.q(ctx).QueryRow(ctx, `SELECT award_badge($1, $2, $3)`, userID, badgeType, raw).Scan(&awarded)
	if err != nil {
		return false, fmt.Errorf("award badge: %w", err)
	}
	return awarded, nil
}

func (r *BadgeRepository) ListByUser(ctx context.Context, userID string) ([]*domain.Badge, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT id, user_id, badge_type, earned_at, COALESCE(metadata, '{}'::jsonb)
		FROM user_badges
		WHERE user_id = $1
		ORDER BY earned_at DESC, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list badges: %w", err)
	}
	defer rows.Close()

	var out []*domain.Badge
	for rows.Next() {
		var b domain.Badge
		var raw []byte
		if err := rows.Scan(&b.ID, &b.UserID, &b.BadgeType, &b.EarnedAt, &raw); err != nil {
			return nil, fmt.Errorf("scan badge: %w", err)
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &b.Metadata)
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}
