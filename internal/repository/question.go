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

type QuestionRepository struct {
	pool *pgxpool.Pool
}

func NewQuestionRepository(pool *pgxpool.Pool) *QuestionRepository {
	return &QuestionRepository{pool: pool}
}

func (r *QuestionRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const questionColumns = `id, event_id, user_id, body, pinned, answer, created_at, updated_at`

func scanQuestion(row pgx.Row) (*domain.Question, error) {
	var q domain.Question
	err := row.Scan(&q.ID, &q.EventID, &q.UserID, &q.Body, &q.Pinned, &q.Answer, &q.CreatedAt, &q.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan question: %w", err)
	}
	return &q, nil
}

func (r *QuestionRepository) Create(ctx context.Context, q *domain.Question) (*domain.Question, error) {
	return scanQuestion(r.q(ctx).QueryRow(ctx, `
		INSERT INTO event_questions (event_id, user_id, body)
		VALUES ($1, $2, $3)
		RETURNING `+questionColumns,
		q.EventID, q.UserID, q.Body))
}

func (r *QuestionRepository) GetByID(ctx context.Context, id string) (*domain.Question, error) {
	return scanQuestion(r.q(ctx).QueryRow(ctx, `
		SELECT `+questionColumns+`
		FROM event_questions WHERE id = $1`, id))
}

func (r *QuestionRepository) SetPinned(ctx context.Context, id string, pinned bool) error {
	tag, err := r.q(ctx).Exec(ctx, `
		UPDATE event_questions SET pinned = $2, updated_at = now() WHERE id = $1`, id, pinned)
	if err != nil {
		return fmt.Errorf("set pinned: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return shared.ErrNotFound
	}
	return nil
}

func (r *QuestionRepository) SetAnswer(ctx context.Context, id string, answer *string) error {
	tag, err := r.q(ctx).Exec(ctx, `
		UPDATE event_questions SET answer = $2, updated_at = now() WHERE id = $1`, id, answer)
	if err != nil {
		return fmt.Errorf("set answer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return shared.ErrNotFound
	}
	return nil
}

func (r *QuestionRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.q(ctx).Exec(ctx, `DELETE FROM event_questions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete question: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return shared.ErrNotFound
	}
	return nil
}

func (r *QuestionRepository) AddVote(ctx context.Context, questionID, userID string) error {
	_, err := r.q(ctx).Exec(ctx, `
		INSERT INTO event_question_votes (question_id, user_id)
		VALUES ($1, $2)
		ON CONFLICT (question_id, user_id) DO NOTHING`, questionID, userID)
	if err != nil {
		return fmt.Errorf("add vote: %w", err)
	}
	return nil
}

func (r *QuestionRepository) RemoveVote(ctx context.Context, questionID, userID string) error {
	_, err := r.q(ctx).Exec(ctx, `
		DELETE FROM event_question_votes
		WHERE question_id = $1 AND user_id = $2`, questionID, userID)
	if err != nil {
		return fmt.Errorf("remove vote: %w", err)
	}
	return nil
}

func (r *QuestionRepository) ListByEvent(ctx context.Context, eventID, viewerID string, limit, offset int) ([]*domain.QuestionWithVote, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT q.id, q.event_id, q.user_id, q.body, q.pinned, q.answer, q.created_at, q.updated_at,
		       count(v.question_id) AS votes,
		       coalesce(bool_or(v.user_id = NULLIF($2, '')::uuid), false) AS voted_by_me
		FROM event_questions q
		LEFT JOIN event_question_votes v ON v.question_id = q.id
		WHERE q.event_id = $1
		GROUP BY q.id
		ORDER BY q.pinned DESC, votes DESC, q.created_at ASC, q.id
		LIMIT $3 OFFSET $4`, eventID, viewerID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list questions: %w", err)
	}
	defer rows.Close()

	var out []*domain.QuestionWithVote
	for rows.Next() {
		var qv domain.QuestionWithVote
		if err := rows.Scan(&qv.ID, &qv.EventID, &qv.UserID, &qv.Body, &qv.Pinned, &qv.Answer, &qv.CreatedAt, &qv.UpdatedAt, &qv.Votes, &qv.VotedByMe); err != nil {
			return nil, fmt.Errorf("scan question: %w", err)
		}
		out = append(out, &qv)
	}
	return out, rows.Err()
}

func (r *QuestionRepository) CountByEvent(ctx context.Context, eventID string) (int, error) {
	var total int
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) FROM event_questions WHERE event_id = $1`, eventID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("count questions: %w", err)
	}
	return total, nil
}
