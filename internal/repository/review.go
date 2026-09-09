package repository

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReviewRepository manages event reviews. Soft delete via UPDATE (owner still
// sees their own row under the reviews_select policy, so RLS allows it).
type ReviewRepository struct {
	pool *pgxpool.Pool
}

func NewReviewRepository(pool *pgxpool.Pool) *ReviewRepository {
	return &ReviewRepository{pool: pool}
}

func (r *ReviewRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const reviewColumns = "id, event_id, user_id, rating, COALESCE(body, ''), status, created_at, updated_at"

func scanReview(row pgx.Row) (*domain.Review, error) {
	var r domain.Review
	err := row.Scan(&r.ID, &r.EventID, &r.UserID, &r.Rating, &r.Body, &r.Status, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan review: %w", err)
	}
	return &r, nil
}

func (r *ReviewRepository) Create(ctx context.Context, rev *domain.Review) (*domain.Review, error) {
	created, err := scanReview(r.q(ctx).QueryRow(ctx, `
		INSERT INTO reviews (event_id, user_id, rating, body)
		VALUES ($1, $2, $3, NULLIF($4, ''))
		RETURNING `+reviewColumns,
		rev.EventID, rev.UserID, rev.Rating, rev.Body))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "reviews_user_id_event_id_key" {
			return nil, shared.WrapAppError(err, "duplicate_review", "You already reviewed this event.", http.StatusConflict)
		}
		return nil, err
	}
	return created, nil
}

func (r *ReviewRepository) GetByID(ctx context.Context, id string) (*domain.Review, error) {
	return scanReview(r.q(ctx).QueryRow(ctx, `
		SELECT `+reviewColumns+`
		FROM reviews WHERE id = $1 AND deleted_at IS NULL`, id))
}

func (r *ReviewRepository) Update(ctx context.Context, id string, rating int16, body string) (*domain.Review, error) {
	return scanReview(r.q(ctx).QueryRow(ctx, `
		UPDATE reviews
		SET rating = $2, body = NULLIF($3, ''), updated_at = now()
		WHERE id = $1
		RETURNING `+reviewColumns, id, rating, body))
}

// SoftDeleteSoft via UPDATE is legal here: the owner still sees their own row
// (reviews_select includes own rows) and public readers stop seeing it because
// status leaves 'published'.
func (r *ReviewRepository) Delete(ctx context.Context, id string) error {
	_, err := r.q(ctx).Exec(ctx, `
		UPDATE reviews
		SET status = 'deleted', deleted_at = now(), updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("delete review: %w", err)
	}
	return nil
}

func (r *ReviewRepository) ListByEvent(ctx context.Context, eventID string, limit, offset int) ([]*domain.Review, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+reviewColumns+`
		FROM reviews
		WHERE event_id = $1 AND status = 'published' AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`, eventID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list reviews: %w", err)
	}
	defer rows.Close()

	var out []*domain.Review
	for rows.Next() {
		rev, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reviews: %w", err)
	}
	return out, nil
}

func (r *ReviewRepository) CountByEvent(ctx context.Context, eventID string) (int, error) {
	var n int
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) FROM reviews
		WHERE event_id = $1 AND status = 'published' AND deleted_at IS NULL`, eventID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count reviews: %w", err)
	}
	return n, nil
}
