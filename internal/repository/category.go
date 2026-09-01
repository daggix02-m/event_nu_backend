package repository

import (
	"context"
	"fmt"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CategoryRepository struct {
	pool *pgxpool.Pool
}

func NewCategoryRepository(pool *pgxpool.Pool) *CategoryRepository {
	return &CategoryRepository{pool: pool}
}

func (r *CategoryRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const categoryColumns = "id, slug, name, sort_order, is_active"

func (r *CategoryRepository) List(ctx context.Context) ([]*domain.Category, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+categoryColumns+`
		FROM categories
		WHERE is_active = true
		ORDER BY sort_order, name`)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()

	var cats []*domain.Category
	for rows.Next() {
		var c domain.Category
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &c.SortOrder, &c.IsActive); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		cats = append(cats, &c)
	}
	return cats, rows.Err()
}