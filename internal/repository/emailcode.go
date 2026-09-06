package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EmailCodeRepository struct {
	pool *pgxpool.Pool
}

func NewEmailCodeRepository(pool *pgxpool.Pool) *EmailCodeRepository {
	return &EmailCodeRepository{pool: pool}
}

// q returns the request transaction (honoring RLS context) when active,
// otherwise the pool.
func (r *EmailCodeRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

// Create stores a one-time code by hash.
func (r *EmailCodeRepository) Create(ctx context.Context, userID, purpose, codeHash string, expiresAt time.Time, maxAttempts int) error {
	_, err := r.q(ctx).Exec(ctx, `
		INSERT INTO email_codes (user_id, purpose, code_hash, expires_at, max_attempts)
		VALUES ($1, $2, $3, $4, $5)`,
		userID, purpose, codeHash, expiresAt, maxAttempts)
	if err != nil {
		return fmt.Errorf("insert email_code: %w", err)
	}
	return nil
}

// Consume attempts to redeem a code. It atomically bumps the attempt counter
// and returns whether the code was valid. On success the row is consumed.
// Returns a typed error for invalid/expired/exhausted cases.
func (r *EmailCodeRepository) Consume(ctx context.Context, codeHash, purpose string) (string, error) {
	tx, isNew, err := database.Tx(ctx, r.pool)
	if err != nil {
		return "", fmt.Errorf("begin code tx: %w", err)
	}
	if isNew {
		defer tx.Rollback(ctx)
	}

	var userID string
	var attempts, maxAttempts int
	var expiresAt time.Time
	var consumedAt *time.Time

	err = tx.QueryRow(ctx, `
		SELECT user_id, attempts, max_attempts, expires_at, consumed_at
		FROM email_codes
		WHERE code_hash = $1 AND purpose = $2`,
		codeHash, purpose).Scan(&userID, &attempts, &maxAttempts, &expiresAt, &consumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", shared.WrapAppError(err, "invalid_code", "This code is invalid.", 400)
	}
	if err != nil {
		return "", fmt.Errorf("scan email_code: %w", err)
	}

	if consumedAt != nil {
		return "", shared.NewAppError("code_used", "This code has already been used.", 400)
	}
	if time.Now().After(expiresAt) {
		return "", shared.NewAppError("code_expired", "This code has expired.", 400)
	}
	if attempts >= maxAttempts {
		return "", shared.NewAppError("code_exhausted", "Too many attempts. Request a new code.", 400)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE email_codes
		SET attempts = attempts + 1, consumed_at = now()
		WHERE code_hash = $1 AND purpose = $2`,
		codeHash, purpose); err != nil {
		return "", fmt.Errorf("update email_code: %w", err)
	}

	if isNew {
		if err := tx.Commit(ctx); err != nil {
			return "", fmt.Errorf("commit email_code: %w", err)
		}
	}
	return userID, nil
}
