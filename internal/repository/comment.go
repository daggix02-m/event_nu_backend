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

type CommentRepository struct {
	pool *pgxpool.Pool
}

func NewCommentRepository(pool *pgxpool.Pool) *CommentRepository {
	return &CommentRepository{pool: pool}
}

// q returns the request transaction (honoring RLS context) when active,
// otherwise the pool.
func (r *CommentRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const commentColumns = `id, event_id, user_id, body, moderation_status, created_at, updated_at, deleted_at`

func scanComment(row pgx.Row) (*domain.Comment, error) {
	var c domain.Comment
	err := row.Scan(
		&c.ID, &c.EventID, &c.UserID, &c.Body,
		&c.ModerationStatus, &c.CreatedAt, &c.UpdatedAt, &c.DeletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan comment: %w", err)
	}
	return &c, nil
}

func (r *CommentRepository) Create(ctx context.Context, c *domain.Comment) (*domain.Comment, error) {
	row := r.q(ctx).QueryRow(ctx, `
		INSERT INTO comments (event_id, user_id, body)
		VALUES ($1, $2, $3)
		RETURNING `+commentColumns, c.EventID, c.UserID, c.Body)
	return scanComment(row)
}

func (r *CommentRepository) GetByID(ctx context.Context, id string) (*domain.Comment, error) {
	row := r.q(ctx).QueryRow(ctx, `
		SELECT `+commentColumns+`
		FROM comments
		WHERE id = $1 AND deleted_at IS NULL`, id)
	return scanComment(row)
}

// UpdateBody edits the body of the comment and bumps updated_at. Ownership is
// enforced by RLS; the service checks it explicitly for a clearer 403.
func (r *CommentRepository) UpdateBody(ctx context.Context, id, body string) (*domain.Comment, error) {
	row := r.q(ctx).QueryRow(ctx, `
		UPDATE comments
		SET body = $2, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING `+commentColumns, id, body)
	return scanComment(row)
}

// Delete removes a comment permanently. Ownership/privilege is enforced by RLS
// (comments_delete_own): a plain user is only allowed to delete their own rows,
// admin/service any. Hard delete is used deliberately — Postgres rejects an
// UPDATE that would make a row invisible to every SELECT policy, so a soft
// delete could never hide a comment from the owner's own reads.
func (r *CommentRepository) Delete(ctx context.Context, id string) error {
	_, err := r.q(ctx).Exec(ctx, `
		DELETE FROM comments
		WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("delete comment: %w", err)
	}
	return nil
}

func (r *CommentRepository) ListByEvent(ctx context.Context, eventID string, limit, offset int) ([]*domain.Comment, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+commentColumns+`
		FROM comments
		WHERE event_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC, id
		LIMIT $2 OFFSET $3`, eventID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	defer rows.Close()

	var out []*domain.Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan comment row: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate comments: %w", err)
	}
	return out, nil
}

func (r *CommentRepository) CountByEvent(ctx context.Context, eventID string) (int, error) {
	var n int
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*)
		FROM comments
		WHERE event_id = $1 AND deleted_at IS NULL`, eventID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count comments: %w", err)
	}
	return n, nil
}
