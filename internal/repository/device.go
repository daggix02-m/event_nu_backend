package repository

import (
	"context"
	"fmt"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DeviceRepository manages the user_devices registry. Registration runs under
// RLS as the authenticated user (own rows only); the push consumer reads the
// full registry as the privileged service role.
type DeviceRepository struct {
	pool *pgxpool.Pool
}

func NewDeviceRepository(pool *pgxpool.Pool) *DeviceRepository {
	return &DeviceRepository{pool: pool}
}

func (r *DeviceRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const deviceColumns = "id, user_id, token, platform, last_seen_at, created_at"

func scanDevice(row pgx.Row) (*domain.Device, error) {
	var d domain.Device
	if err := row.Scan(&d.ID, &d.UserID, &d.Token, &d.Platform, &d.LastSeenAt, &d.CreatedAt); err != nil {
		return nil, fmt.Errorf("scan device: %w", err)
	}
	return &d, nil
}

// Upsert registers the token for the user. One device = one token, so a token
// registered by another user (reinstall on the same phone) is reassigned to
// the caller and its last-seen stamp refreshed. Idempotent. Registration runs
// through the SECURITY DEFINER upsert_user_device (the on-conflict path may
// touch another user's row, which app_user RLS forbids); the function returns
// the freshly registered row via RETURN QUERY.
func (r *DeviceRepository) Upsert(ctx context.Context, userID, token, platform string) (*domain.Device, error) {
	return scanDevice(r.q(ctx).QueryRow(ctx, `
		SELECT out_id, out_user_id, out_token, out_platform, out_last_seen_at, out_created_at
		FROM upsert_user_device($1, $2, $3)`,
		userID, token, platform))
}

// Delete deregisters the caller's device. Idempotent: no error when the id
// belongs to someone else or does not exist.
func (r *DeviceRepository) Delete(ctx context.Context, userID, id string) error {
	_, err := r.q(ctx).Exec(ctx, `
		DELETE FROM user_devices
		WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete device: %w", err)
	}
	return nil
}

// ListByUser returns the caller's devices, newest first.
func (r *DeviceRepository) ListByUser(ctx context.Context, userID string) ([]*domain.Device, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+deviceColumns+`
		FROM user_devices
		WHERE user_id = $1
		ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()

	var out []*domain.Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteByToken prunes a device by its registration token (used by the push
// consumer when the provider reports the token unregistered). Idempotent.
func (r *DeviceRepository) DeleteByToken(ctx context.Context, token string) error {
	_, err := r.q(ctx).Exec(ctx, `
		DELETE FROM user_devices WHERE token = $1`, token)
	if err != nil {
		return fmt.Errorf("prune device: %w", err)
	}
	return nil
}
