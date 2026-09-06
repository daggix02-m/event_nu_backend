package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

// q returns the request transaction (honoring RLS context) when active,
// otherwise the pool.
func (r *UserRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const userColumns = "id, email, password_hash, username, COALESCE(bio, ''), COALESCE(photo_url, ''), role, is_verified, status, created_at, updated_at, deleted_at"

func scanUser(row pgx.Row) (*domain.User, error) {
	var u domain.User
	err := row.Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.Username, &u.Bio, &u.PhotoURL,
		&u.Role, &u.IsVerified, &u.Status, &u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}
	return &u, nil
}

func (r *UserRepository) Create(ctx context.Context, email, passwordHash, username string) (*domain.User, error) {
	row := r.q(ctx).QueryRow(ctx, `
		INSERT INTO users (email, password_hash, username)
		VALUES ($1, $2, $3)
		RETURNING `+userColumns, email, passwordHash, username)

	user, err := scanUser(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			switch pgErr.ConstraintName {
			case "users_email_key":
				return nil, shared.WrapAppError(err, "email_taken", "An account with this email already exists.", 409)
			case "users_username_key":
				return nil, shared.WrapAppError(err, "username_taken", "This username is already taken.", 409)
			}
		}
		return nil, err
	}
	return user, nil
}

func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	row := r.q(ctx).QueryRow(ctx, `
		SELECT `+userColumns+`
		FROM users
		WHERE email = $1 AND deleted_at IS NULL`, email)
	return scanUser(row)
}

func (r *UserRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	row := r.q(ctx).QueryRow(ctx, `
		SELECT `+userColumns+`
		FROM users
		WHERE id = $1 AND deleted_at IS NULL`, id)
	return scanUser(row)
}

func (r *UserRepository) UpdateVerified(ctx context.Context, id string) error {
	_, err := r.q(ctx).Exec(ctx, `
		UPDATE users SET is_verified = true, updated_at = now()
		WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("update user verified: %w", err)
	}
	return nil
}
