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

type SessionRepository struct {
	pool *pgxpool.Pool
}

func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

// q returns the request transaction (honoring RLS context) when active,
// otherwise the pool.
func (r *SessionRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

func (r *SessionRepository) Create(ctx context.Context, s *domain.Session) error {
	_, err := r.q(ctx).Exec(ctx, `
		INSERT INTO auth_sessions (user_id, refresh_hash, user_agent, ip_hash, device_name, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		s.UserID, s.RefreshHash, s.UserAgent, s.IPHash, s.DeviceName, s.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// GetActiveByRefreshHash returns a non-revoked, non-expired session for a
// hashed refresh token.
func (r *SessionRepository) GetActiveByRefreshHash(ctx context.Context, refreshHash string) (*domain.Session, error) {
	row := r.q(ctx).QueryRow(ctx, `
		SELECT id, user_id, refresh_hash, user_agent, ip_hash, device_name, expires_at, revoked_at, created_at, updated_at
		FROM auth_sessions
		WHERE refresh_hash = $1 AND revoked_at IS NULL AND expires_at > now()`, refreshHash)

	var s domain.Session
	err := row.Scan(&s.ID, &s.UserID, &s.RefreshHash, &s.UserAgent, &s.IPHash, &s.DeviceName, &s.ExpiresAt, &s.RevokedAt, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan session: %w", err)
	}
	return &s, nil
}

// Revoke invalidates a refresh token by hash (idempotent).
func (r *SessionRepository) Revoke(ctx context.Context, refreshHash string) error {
	_, err := r.q(ctx).Exec(ctx, `
		UPDATE auth_sessions SET revoked_at = now(), updated_at = now()
		WHERE refresh_hash = $1 AND revoked_at IS NULL`, refreshHash)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

// RevokeUserSessions revokes all active sessions for a user (used on logout-all
// or account actions).
func (r *SessionRepository) RevokeUserSessions(ctx context.Context, userID string) error {
	_, err := r.q(ctx).Exec(ctx, `
		UPDATE auth_sessions SET revoked_at = now(), updated_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	if err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	return nil
}
