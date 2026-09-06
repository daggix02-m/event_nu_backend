package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/email"
	"github.com/daggix02-m/event_nu/backend/internal/repository"
)

// EmailConsumer drains the email_outbox table and sends via the configured
// provider. It owns retry/backoff; failed sends are dead-lettered (status
// 'failed') after max attempts and remain visible in email_logs.
type EmailConsumer struct {
	logger *slog.Logger
	cfg    config.Config
	outbox *repository.OutboxRepository
	sender email.Sender
}

func NewEmailConsumer(logger *slog.Logger, cfg config.Config, outbox *repository.OutboxRepository, sender email.Sender) *EmailConsumer {
	return &EmailConsumer{logger: logger, cfg: cfg, outbox: outbox, sender: sender}
}

func (c *EmailConsumer) Run(ctx context.Context) error {
	c.logger.Info("email consumer started", "poll_interval", c.cfg.EmailPollInterval.String(), "batch_size", c.cfg.EmailBatchSize)
	ticker := time.NewTicker(c.cfg.EmailPollInterval)
	defer ticker.Stop()

	for {
		if err := c.processBatch(ctx); err != nil && ctx.Err() == nil {
			c.logger.Error("email batch failed", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			c.logger.Info("email consumer stopped")
			return nil
		case <-ticker.C:
		}
	}
}

func (c *EmailConsumer) processBatch(ctx context.Context) error {
	items, err := c.outbox.ClaimBatch(ctx, c.cfg.EmailBatchSize, 5*time.Minute)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}

	c.logger.Debug("claimed email batch", "count", len(items))
	for _, item := range items {
		if err := c.sendOne(ctx, item); err != nil && ctx.Err() == nil {
			c.logger.Error("email send failed", "outbox_id", item.ID, "error", err.Error())
		}
	}
	return nil
}

func (c *EmailConsumer) sendOne(ctx context.Context, item domain.EmailOutbox) error {
	attempt := item.Attempts + 1

	messageID, err := c.sender.Send(ctx, email.Message{
		RecipientName:  item.RecipientName,
		RecipientEmail: item.RecipientEmail,
		TemplateID:     item.TemplateID,
		Params:         item.Params,
	})
	if err == nil {
		c.logger.Info("email sent", "outbox_id", item.ID, "to", item.RecipientEmail, "template_id", item.TemplateID, "message_id", messageID)
		return c.outbox.MarkSent(ctx, item.ID, messageID)
	}

	var brevoErr *email.BrevoError
	retryable := errors.As(err, &brevoErr) && brevoErr.Retryable()
	if attempt < item.MaxAttempts && retryable {
		next := time.Now().Add(c.cfg.EmailRetryBaseDelay * time.Duration(1<<min(attempt-1, 6)))
		c.logger.Warn("email retry scheduled", "outbox_id", item.ID, "attempt", attempt, "retry_at", next.String())
		return c.outbox.MarkFailed(ctx, item.ID, err.Error(), attempt, item.MaxAttempts, next)
	}

	// Permanent failure or exhausted retries: dead-letter immediately by
	// counting the attempt as terminal (status -> 'failed').
	c.logger.Error("email dead-lettered", "outbox_id", item.ID, "attempt", attempt, "error", err.Error())
	return c.outbox.MarkFailed(ctx, item.ID, err.Error(), item.MaxAttempts, item.MaxAttempts, time.Now())
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
