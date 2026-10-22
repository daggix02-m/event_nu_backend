package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/daggix02-m/event_nu_backend/internal/domain"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu_backend/internal/shared"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserRepository struct {
	pool *pgxpool.Pool
}

// Compile-time proof that the repository adapter implements the core's
// driven port (hexagonal: adapters depend on ports, never the reverse).
var _ domain.UserVerificationStore = (*UserRepository)(nil)

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

// Compile-time proof that the repository adapter implements the core's
// driven port (hexagonal: adapters depend on ports, never the reverse).
var _ domain.UserStore = (*UserRepository)(nil)

// q returns the request transaction (honoring RLS context) when active,
// otherwise the pool.
func (r *UserRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const userColumns = "id, email, password_hash, username, COALESCE(bio, ''), COALESCE(photo_url, ''), role, is_verified, status, created_at, updated_at, deleted_at, onboarding_completed_at"

func scanUser(row pgx.Row) (*domain.User, error) {
	var u domain.User
	err := row.Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.Username, &u.Bio, &u.PhotoURL,
		&u.Role, &u.IsVerified, &u.Status, &u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
		&u.OnboardingCompletedAt,
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

func (r *UserRepository) UpdateProfile(ctx context.Context, id string, username, bio, photoURL *string) (*domain.User, error) {
	row := r.q(ctx).QueryRow(ctx, `
		UPDATE users
		SET username  = COALESCE($2, username),
		    bio       = COALESCE($3, bio),
		    photo_url = COALESCE($4, photo_url),
		    updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING `+userColumns, id, username, bio, photoURL)

	user, err := scanUser(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "users_username_key" {
			return nil, shared.WrapAppError(err, "username_taken", "This username is already taken.", 409)
		}
		return nil, err
	}
	return user, nil
}

func (r *UserRepository) CompleteOnboarding(ctx context.Context, id string) (*domain.User, error) {
	row := r.q(ctx).QueryRow(ctx, `
		UPDATE users
		SET onboarding_completed_at = COALESCE(onboarding_completed_at, now()),
		    updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING `+userColumns, id)
	return scanUser(row)
}

func adminUserClause(q, role, status string) (string, []any) {
	conds := []string{"deleted_at IS NULL"}
	args := []any{}
	if role != "" {
		args = append(args, role)
		conds = append(conds, fmt.Sprintf("role = $%d", len(args)))
	}
	if status != "" {
		args = append(args, status)
		conds = append(conds, fmt.Sprintf("status = $%d", len(args)))
	}
	if trimmed := strings.TrimSpace(q); trimmed != "" {
		args = append(args, escapeLikePattern(trimmed))
		conds = append(conds, fmt.Sprintf("(email ILIKE '%%' || $%d || '%%' OR username ILIKE '%%' || $%d || '%%')", len(args), len(args)))
	}
	return strings.Join(conds, " AND "), args
}

// ListAdmin returns users matching the optional q/role/status filters, newest
// first. Runs under the admin RLS role, so every visible row (any role or
// status) is returned.
func (r *UserRepository) ListAdmin(ctx context.Context, q, role, status string, limit, offset int) ([]*domain.User, error) {
	where, args := adminUserClause(q, role, status)
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+userColumns+`
		FROM users
		WHERE `+where+`
		ORDER BY created_at DESC, id
		LIMIT $`+numArg(len(args)+1)+` OFFSET $`+numArg(len(args)+2),
		append(args, limit, offset)...)
	if err != nil {
		return nil, fmt.Errorf("admin list users: %w", err)
	}
	defer rows.Close()

	var users []*domain.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (r *UserRepository) CountAdmin(ctx context.Context, q, role, status string) (int, error) {
	where, args := adminUserClause(q, role, status)
	var total int
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) FROM users WHERE `+where, args...).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("admin count users: %w", err)
	}
	return total, nil
}

func (r *UserRepository) UpdateAdmin(ctx context.Context, id string, username, bio, photoURL, role, status *string) (*domain.User, error) {
	row := r.q(ctx).QueryRow(ctx, `
		UPDATE users
		SET username  = COALESCE($2, username),
		    bio       = COALESCE($3, bio),
		    photo_url = COALESCE($4, photo_url),
		    role      = COALESCE($5::user_role, role),
		    status    = COALESCE($6::user_status, status),
		    updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING `+userColumns, id, username, bio, photoURL, role, status)

	user, err := scanUser(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "users_username_key" {
			return nil, shared.WrapAppError(err, "username_taken", "This username is already taken.", 409)
		}
		return nil, err
	}
	return user, nil
}

// SetStatus flips a user's lifecycle status (active/suspended/deactivated).
// Idempotent: setting the current status is a no-op UPDATE that still returns
// the row.
func (r *UserRepository) SetStatus(ctx context.Context, id, status string) (*domain.User, error) {
	row := r.q(ctx).QueryRow(ctx, `
		UPDATE users
		SET status = $2::user_status, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING `+userColumns, id, status)
	return scanUser(row)
}
