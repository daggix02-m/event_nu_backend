package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrIdempotencyExists is returned by Create when the (user_id, key) pair is
// already present — either a concurrent in-progress write or a prior completed
// request. The caller inspects the existing row to decide replay vs reuse-409.
var ErrIdempotencyExists = errors.New("idempotency key already exists")

const (
	IdempotencyInProgress = "in_progress"
	IdempotencyCompleted  = "completed"
)

// IdempotencyRecord is a single stored idempotency key row.
type IdempotencyRecord struct {
	UserID       string
	Key          string
	RequestHash  string
	Operation    string
	Status       string
	ResponseCode int
	ResponseBody json.RawMessage
	ExpiresAt    time.Time
}

type IdempotencyRepository struct {
	pool *pgxpool.Pool
}

func NewIdempotencyRepository(pool *pgxpool.Pool) *IdempotencyRepository {
	return &IdempotencyRepository{pool: pool}
}

func (r *IdempotencyRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

// Create records a new in-progress idempotency key for the user. It first
// opportunistically reaps the user's expired keys so a stale in-progress row
// (e.g. one abandoned by a server failure) does not permanently trap the key.
// It returns ErrIdempotencyExists when (user_id, key) is already taken.
//
// The insert uses ON CONFLICT DO NOTHING (not a plain INSERT) on purpose: a
// competing unique-violation error would abort the surrounding transaction,
// making the caller's follow-up lookup of the existing row fail. DO NOTHING
// suppresses the conflict and reports zero rows instead, so the transaction
// stays usable. Concurrent writers still serialize on the unique index: a
// competitor's uncommitted row blocks until it commits (we then see it as
// existing) or rolls back (we then insert).
func (r *IdempotencyRepository) Create(ctx context.Context, userID, key, requestHash, operation string, ttl time.Duration) error {
	_, err := r.q(ctx).Exec(ctx, `DELETE FROM idempotency_keys WHERE user_id = $1 AND expires_at < now()`, userID)
	if err != nil {
		return fmt.Errorf("reap expired idempotency keys: %w", err)
	}

	expiresAt := time.Now().Add(ttl)
	tag, err := r.q(ctx).Exec(ctx, `
		INSERT INTO idempotency_keys (user_id, key, request_hash, operation, status, expires_at)
		VALUES ($1, $2, $3, $4, 'in_progress', $5)
		ON CONFLICT (user_id, key) DO NOTHING`,
		userID, key, requestHash, operation, expiresAt)
	if err != nil {
		return fmt.Errorf("insert idempotency key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", ErrIdempotencyExists, "user_id,key")
	}
	return nil
}

// Get returns a stored idempotency record for the user+key, or shared.ErrNotFound.
func (r *IdempotencyRepository) Get(ctx context.Context, userID, key string) (*IdempotencyRecord, error) {
	row := r.q(ctx).QueryRow(ctx, `
		SELECT user_id, key, request_hash, operation, status,
		       COALESCE(response_code, 0), COALESCE(response_body, 'null'::jsonb), expires_at
		FROM idempotency_keys
		WHERE user_id = $1 AND key = $2`, userID, key)

	var rec IdempotencyRecord
	err := row.Scan(&rec.UserID, &rec.Key, &rec.RequestHash, &rec.Operation, &rec.Status,
		&rec.ResponseCode, &rec.ResponseBody, &rec.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan idempotency key: %w", err)
	}
	return &rec, nil
}

// Complete marks a previously in-progress key as completed with the stored
// status and response body. Runs in the same request transaction as the write
// it guards, so a later rollback undoes the record too.
func (r *IdempotencyRepository) Complete(ctx context.Context, userID, key, status string, code int, body []byte) error {
	_, err := r.q(ctx).Exec(ctx, `
		UPDATE idempotency_keys
		SET status = $3, response_code = $4, response_body = $5
		WHERE user_id = $1 AND key = $2`,
		userID, key, status, code, body)
	if err != nil {
		return fmt.Errorf("complete idempotency key: %w", err)
	}
	return nil
}
