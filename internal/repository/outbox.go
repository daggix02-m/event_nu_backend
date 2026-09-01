package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OutboxRepository struct {
	pool *pgxpool.Pool
}

func NewOutboxRepository(pool *pgxpool.Pool) *OutboxRepository {
	return &OutboxRepository{pool: pool}
}

// Create enqueues an email. Handlers INSERT and return — the worker owns the
// actual send. idempotencyKey is optional (empty for fire-and-forget emails).
func (r *OutboxRepository) Create(ctx context.Context, m domain.EmailOutbox) error {
	params, err := json.Marshal(m.Params)
	if err != nil {
		return fmt.Errorf("marshal outbox params: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO email_outbox
			(recipient_email, recipient_name, template_id, params, max_attempts, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		m.RecipientEmail, m.RecipientName, m.TemplateID, params, m.MaxAttempts, nullIfEmpty(m.IdempotencyKey))
	if err != nil {
		return fmt.Errorf("insert email_outbox: %w", err)
	}
	return nil
}

// ClaimBatch atomically claims due rows, preventing double-sends by
// concurrent workers. Rows are leased via next_retry_at so a crash mid-send
// re-queues them after the lease expires.
func (r *OutboxRepository) ClaimBatch(ctx context.Context, batchSize int, lease time.Duration) ([]domain.EmailOutbox, error) {
	rows, err := r.pool.Query(ctx, `
		UPDATE email_outbox
		SET next_retry_at = now() + $2, updated_at = now()
		WHERE id IN (
			SELECT id FROM email_outbox
			WHERE status = 'pending' AND next_retry_at <= now()
			ORDER BY created_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, recipient_email, recipient_name, template_id, params, attempts, max_attempts, next_retry_at`,
		batchSize, lease.String())
	if err != nil {
		return nil, fmt.Errorf("claim email batch: %w", err)
	}
	defer rows.Close()

	var items []domain.EmailOutbox
	for rows.Next() {
		var m domain.EmailOutbox
		var rawParams []byte
		if err := rows.Scan(&m.ID, &m.RecipientEmail, &m.RecipientName, &m.TemplateID, &rawParams, &m.Attempts, &m.MaxAttempts, &m.NextRetryAt); err != nil {
			return nil, fmt.Errorf("scan email batch: %w", err)
		}
		if err := json.Unmarshal(rawParams, &m.Params); err != nil {
			return nil, fmt.Errorf("unmarshal outbox params: %w", err)
		}
		items = append(items, m)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("iterate email batch: %w", rows.Err())
	}
	return items, nil
}

// MarkSent records a successful send and appends to email_logs.
func (r *OutboxRepository) MarkSent(ctx context.Context, id, messageID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin sent tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE email_outbox
		SET status = 'sent', brevo_message_id = $2, sent_at = now(), updated_at = now()
		WHERE id = $1`, id, messageID); err != nil {
		return fmt.Errorf("mark outbox sent: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO email_logs (outbox_id, recipient_email, template_id, status, brevo_message_id)
		SELECT id, recipient_email, template_id, 'sent', $2 FROM email_outbox WHERE id = $1`, id, messageID); err != nil {
		return fmt.Errorf("log email sent: %w", err)
	}

	return tx.Commit(ctx)
}

// MarkFailed records a failure and schedules the next attempt. When attempts
// are exhausted the row is marked failed (dead-lettered) and logged.
func (r *OutboxRepository) MarkFailed(ctx context.Context, id string, errMsg string, attempts, maxAttempts int, nextRetryAt time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin failed tx: %w", err)
	}
	defer tx.Rollback(ctx)

	status := "pending"
	if attempts >= maxAttempts {
		status = "failed"
	}

	if _, err := tx.Exec(ctx, `
		UPDATE email_outbox
		SET attempts = $2, last_error = $3, next_retry_at = $4, status = $5, updated_at = now()
		WHERE id = $1`,
		id, attempts, errMsg, nextRetryAt, status); err != nil {
		return fmt.Errorf("mark outbox failed: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO email_logs (outbox_id, recipient_email, template_id, status, error)
		SELECT id, recipient_email, template_id, $2, $3 FROM email_outbox WHERE id = $1`,
		id, status, errMsg); err != nil {
		return fmt.Errorf("log email failed: %w", err)
	}

	return tx.Commit(ctx)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}