package repository

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReminderRepository manages event reminders (record-only; dispatch is Phase 17).
type ReminderRepository struct {
	pool *pgxpool.Pool
}

func NewReminderRepository(pool *pgxpool.Pool) *ReminderRepository {
	return &ReminderRepository{pool: pool}
}

func (r *ReminderRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const reminderColumns = "id, event_id, user_id, remind_at, status, created_at"

func scanReminder(row pgx.Row) (*domain.Reminder, error) {
	var x domain.Reminder
	err := row.Scan(&x.ID, &x.EventID, &x.UserID, &x.RemindAt, &x.Status, &x.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("scan reminder: %w", err)
	}
	return &x, nil
}

func (r *ReminderRepository) Create(ctx context.Context, rem *domain.Reminder) (*domain.Reminder, error) {
	created, err := scanReminder(r.q(ctx).QueryRow(ctx, `
		INSERT INTO event_reminders (event_id, user_id, remind_at)
		VALUES ($1, $2, $3)
		RETURNING `+reminderColumns, rem.EventID, rem.UserID, rem.RemindAt))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, shared.WrapAppError(err, "reminder_exists", "You already set a reminder for this event at that time.", http.StatusConflict)
		}
		return nil, err
	}
	return created, nil
}

// Delete removes the caller's reminder for an event. Idempotent.
func (r *ReminderRepository) Delete(ctx context.Context, userID, eventID string) error {
	_, err := r.q(ctx).Exec(ctx, `
		DELETE FROM event_reminders
		WHERE user_id = $1 AND event_id = $2`, userID, eventID)
	if err != nil {
		return fmt.Errorf("delete reminder: %w", err)
	}
	return nil
}

// ListByUser returns the caller's reminders joined onto event summaries
// (nullable when the event is no longer publicly visible under RLS).
func (r *ReminderRepository) ListByUser(ctx context.Context, userID string, limit, offset int) ([]*domain.ReminderWithEvent, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT rem.id, rem.event_id, rem.user_id, rem.remind_at, rem.status, rem.created_at,
		       COALESCE(ev.title, ''),
		       COALESCE(ev.starts_at, to_timestamp(0)) AS event_starts_at
		FROM event_reminders rem
		LEFT JOIN events ev ON ev.id = rem.event_id
		WHERE rem.user_id = $1
		ORDER BY rem.remind_at, rem.id
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list reminders: %w", err)
	}
	defer rows.Close()

	var out []*domain.ReminderWithEvent
	for rows.Next() {
		var x domain.ReminderWithEvent
		if err := rows.Scan(&x.ID, &x.EventID, &x.UserID, &x.RemindAt, &x.Status, &x.CreatedAt,
			&x.EventTitle, &x.EventTime); err != nil {
			return nil, fmt.Errorf("scan reminder row: %w", err)
		}
		if x.EventTime != nil && x.EventTime.Equal(time.Unix(0, 0)) {
			x.EventTime = nil
		}
		out = append(out, &x)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reminders: %w", err)
	}
	return out, nil
}

func (r *ReminderRepository) CountByUser(ctx context.Context, userID string) (int, error) {
	var total int
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) FROM event_reminders WHERE user_id = $1`, userID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("count reminders: %w", err)
	}
	return total, nil
}

// ClaimDue atomically claims a batch of scheduled reminders whose remind_at has
// arrived, leasing them so a crash mid-dispatch re-queues them after the lease
// expires. Runs as the privileged service role.
func (r *ReminderRepository) ClaimDue(ctx context.Context, batchSize int, lease time.Duration) ([]*domain.Reminder, error) {
	rows, err := r.q(ctx).Query(ctx, `
		UPDATE event_reminders
		SET dispatch_lease_at = now() + $2
		WHERE id IN (
			SELECT id FROM event_reminders
			WHERE status = 'scheduled'
			  AND remind_at <= now()
			  AND (dispatch_lease_at IS NULL OR dispatch_lease_at <= now())
			ORDER BY remind_at, id
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING `+reminderColumns, batchSize, lease.String())
	if err != nil {
		return nil, fmt.Errorf("claim due reminders: %w", err)
	}
	defer rows.Close()

	var out []*domain.Reminder
	for rows.Next() {
		rem, err := scanReminder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rem)
	}
	return out, rows.Err()
}

// MarkReminderSent records dispatch and links the notification that was
// created for the user's inbox; clears the lease.
func (r *ReminderRepository) MarkReminderSent(ctx context.Context, id, notificationID string) error {
	_, err := r.q(ctx).Exec(ctx, `
		UPDATE event_reminders
		SET status = 'sent', sent_at = now(), notification_id = $2, dispatch_lease_at = NULL
		WHERE id = $1`, id, notificationID)
	if err != nil {
		return fmt.Errorf("mark reminder sent: %w", err)
	}
	return nil
}

// MarkReminderFailed records a dispatch failure; clears the lease.
func (r *ReminderRepository) MarkReminderFailed(ctx context.Context, id string) error {
	_, err := r.q(ctx).Exec(ctx, `
		UPDATE event_reminders
		SET status = 'failed', dispatch_lease_at = NULL
		WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark reminder failed: %w", err)
	}
	return nil
}
