package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NotificationRepository is the read/write surface for the notification inbox.
// Inserts go through the SECURITY DEFINER notify_user helper (RLS forbids an
// app_user from inserting a row for a different user).
type NotificationRepository struct {
	pool *pgxpool.Pool
}

func NewNotificationRepository(pool *pgxpool.Pool) *NotificationRepository {
	return &NotificationRepository{pool: pool}
}

func (r *NotificationRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const notificationColumns = "id, user_id, type, title, body, COALESCE(data, '{}'::jsonb), read_at, created_at"

func scanNotification(row pgx.Row) (*domain.Notification, error) {
	var n domain.Notification
	var raw []byte
	err := row.Scan(&n.ID, &n.UserID, &n.Type, &n.Title, &n.Body, &raw, &n.ReadAt, &n.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("scan notification: %w", err)
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &n.Data)
	}
	return &n, nil
}

// Notify inserts a notification for a user via notify_user (owns its own rows,
// so it runs outside the request's RLS identity) and returns the new id.
func (r *NotificationRepository) Notify(ctx context.Context, userID, ntype, title, body string, data map[string]any) (string, error) {
	raw, _ := json.Marshal(data)
	var id string
	err := r.q(ctx).QueryRow(ctx, `SELECT notify_user($1, $2, $3, $4, $5)`, userID, ntype, title, body, raw).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("notify: %w", err)
	}
	return id, nil
}

func (r *NotificationRepository) ListByUser(ctx context.Context, userID string, limit, offset int) ([]*domain.Notification, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+notificationColumns+`
		FROM notifications
		WHERE user_id = $1
		ORDER BY created_at DESC, id
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()

	var out []*domain.Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (r *NotificationRepository) CountByUser(ctx context.Context, userID string) (int, error) {
	var total int
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) FROM notifications WHERE user_id = $1`, userID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("count notifications: %w", err)
	}
	return total, nil
}

// MarkRead marks a single notification read. Returns ErrNotFound when the id is
// not one of the caller's notifications (already-read rows are treated as
// found so the endpoint is idempotent).
func (r *NotificationRepository) MarkRead(ctx context.Context, userID, id string) error {
	err := r.q(ctx).QueryRow(ctx, `
		UPDATE notifications SET read_at = now()
		WHERE id = $1 AND user_id = $2
		RETURNING id`, id, userID).Scan(new(string))
	if errors.Is(err, pgx.ErrNoRows) {
		return shared.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("mark notification read: %w", err)
	}
	return nil
}

// MarkAllRead marks every unread notification for the user read; returns how
// many were updated.
func (r *NotificationRepository) MarkAllRead(ctx context.Context, userID string) (int64, error) {
	tag, err := r.q(ctx).Exec(ctx, `
		UPDATE notifications SET read_at = now()
		WHERE user_id = $1 AND read_at IS NULL`, userID)
	if err != nil {
		return 0, fmt.Errorf("mark all notifications read: %w", err)
	}
	return tag.RowsAffected(), nil
}

const dispatchNotificationColumns = "id, user_id, type, title, body, COALESCE(data, '{}'::jsonb), dispatch_attempts, next_dispatch_at"

// ClaimForDispatch atomically claims a batch of undispatched notifications,
// leasing them so a crash mid-send re-queues them after the lease expires.
// runs as the privileged service role.
func (r *NotificationRepository) ClaimForDispatch(ctx context.Context, batchSize int, lease time.Duration) ([]*domain.Notification, error) {
	rows, err := r.q(ctx).Query(ctx, `
		UPDATE notifications
		SET dispatch_lease_at = now() + $2
		WHERE id IN (
			SELECT id FROM notifications
			WHERE dispatched_at IS NULL
			  AND next_dispatch_at <= now()
			  AND (dispatch_lease_at IS NULL OR dispatch_lease_at <= now())
			ORDER BY created_at, id
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING `+dispatchNotificationColumns,
		batchSize, lease.String())
	if err != nil {
		return nil, fmt.Errorf("claim notifications: %w", err)
	}
	defer rows.Close()

	var out []*domain.Notification
	for rows.Next() {
		var n domain.Notification
		var raw []byte
		if err := rows.Scan(&n.ID, &n.UserID, &n.Type, &n.Title, &n.Body, &raw, &n.DispatchAttempts, &n.NextDispatchAt); err != nil {
			return nil, fmt.Errorf("scan claimed notification: %w", err)
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &n.Data)
		}
		out = append(out, &n)
	}
	return out, rows.Err()
}

// MarkDispatched records successful dispatch (or "nothing to deliver" when the
// recipient has no devices) and clears the lease.
func (r *NotificationRepository) MarkDispatched(ctx context.Context, id string) error {
	_, err := r.q(ctx).Exec(ctx, `
		UPDATE notifications
		SET dispatched_at = now(), dispatch_lease_at = NULL, next_dispatch_at = now()
		WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark notification dispatched: %w", err)
	}
	return nil
}

// MarkDispatchFailed schedules the next attempt (exponential backoff is the
// caller's job via nextRetryAt) and clears the lease. When attempts are
// exhausted the caller passes the same value again so the row stops being
// re-claimed.
func (r *NotificationRepository) MarkDispatchFailed(ctx context.Context, id string, attempts int, nextRetryAt time.Time) error {
	_, err := r.q(ctx).Exec(ctx, `
		UPDATE notifications
		SET dispatch_attempts = $2, next_dispatch_at = $3, dispatch_lease_at = NULL
		WHERE id = $1`, id, attempts, nextRetryAt)
	if err != nil {
		return fmt.Errorf("mark notification dispatch failed: %w", err)
	}
	return nil
}
