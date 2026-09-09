package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
// so it runs outside the request's RLS identity).
func (r *NotificationRepository) Notify(ctx context.Context, userID, ntype, title, body string, data map[string]any) error {
	raw, _ := json.Marshal(data)
	_, err := r.q(ctx).Exec(ctx, `SELECT notify_user($1, $2, $3, $4, $5)`, userID, ntype, title, body, raw)
	if err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	return nil
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
