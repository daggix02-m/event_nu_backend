package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/push"
)

// notificationQueueStore is the persistence surface the push consumer needs
// for notification dispatch. It is an interface (rather than the concrete
// repositories) so worker tests can inject fakes without a database.
type notificationQueueStore interface {
	ClaimForDispatch(ctx context.Context, batchSize int, lease time.Duration) ([]*domain.Notification, error)
	MarkDispatched(ctx context.Context, id string) error
	MarkDispatchFailed(ctx context.Context, id string, attempts int, nextRetryAt time.Time) error
	Notify(ctx context.Context, userID, ntype, title, body string, data map[string]any) (string, error)
}

type reminderQueueStore interface {
	ClaimDue(ctx context.Context, batchSize int, lease time.Duration) ([]*domain.Reminder, error)
	MarkReminderSent(ctx context.Context, id, notificationID string) error
	MarkReminderFailed(ctx context.Context, id string) error
}

type pushDeviceStore interface {
	ListByUser(ctx context.Context, userID string) ([]*domain.Device, error)
	DeleteByToken(ctx context.Context, token string) error
}

type reminderEventStore interface {
	GetByID(ctx context.Context, id string) (*domain.Event, error)
}

// PushConsumer drains the notifications table (created by the Phase 12d hooks)
// and the due event reminders, sending each to every device of the recipient
// via the push provider. Failure handling mirrors the email consumer:
// transient provider failures back off through next_dispatch_at (capped by
// PUSH_MAX_ATTEMPTS); provider-reported invalid tokens prune the device so it
// is never retried. A recipient with no devices is marked dispatched — there
// is nothing to deliver and re-scanning forever would burn the poll.
type PushConsumer struct {
	logger    *slog.Logger
	cfg       config.Config
	queue     notificationQueueStore
	reminders reminderQueueStore
	devices   pushDeviceStore
	events    reminderEventStore
	sender    push.Provider
}

func NewPushConsumer(
	logger *slog.Logger,
	cfg config.Config,
	queue notificationQueueStore,
	reminders reminderQueueStore,
	devices pushDeviceStore,
	events reminderEventStore,
	sender push.Provider,
) *PushConsumer {
	return &PushConsumer{logger: logger, cfg: cfg, queue: queue, reminders: reminders, devices: devices, events: events, sender: sender}
}

func (c *PushConsumer) Run(ctx context.Context) error {
	c.logger.Info("push consumer started", "poll_interval", c.cfg.PushPollInterval.String(), "batch_size", c.cfg.PushBatchSize, "provider", c.sender.Name())
	ticker := time.NewTicker(c.cfg.PushPollInterval)
	defer ticker.Stop()

	for {
		if err := c.processNotifications(ctx); err != nil && ctx.Err() == nil {
			c.logger.Error("push notification batch failed", "error", err.Error())
		}
		if err := c.processReminders(ctx); err != nil && ctx.Err() == nil {
			c.logger.Error("push reminder batch failed", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			c.logger.Info("push consumer stopped")
			return nil
		case <-ticker.C:
		}
	}
}

func (c *PushConsumer) processNotifications(ctx context.Context) error {
	items, err := c.queue.ClaimForDispatch(ctx, c.cfg.PushBatchSize, 5*time.Minute)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	c.logger.Debug("claimed push batch", "count", len(items))
	for _, n := range items {
		c.dispatchNotification(ctx, n)
	}
	return nil
}

func (c *PushConsumer) dispatchNotification(ctx context.Context, n *domain.Notification) {
	devices, err := c.devices.ListByUser(ctx, n.UserID)
	if err != nil {
		c.logger.Error("list recipient devices failed", "notification_id", n.ID, "error", err.Error())
		c.bumpRetry(ctx, n)
		return
	}
	if len(devices) == 0 {
		if err := c.queue.MarkDispatched(ctx, n.ID); err != nil {
			c.logger.Error("mark notification dispatched (no devices)", "notification_id", n.ID, "error", err.Error())
		}
		return
	}

	pending := 0
	for _, d := range devices {
		if c.sendOne(ctx, n, d) != nil {
			pending++
		}
	}
	if pending == 0 || n.DispatchAttempts+1 >= c.cfg.PushMaxAttempts {
		if err := c.queue.MarkDispatched(ctx, n.ID); err != nil {
			c.logger.Error("mark notification dispatched", "notification_id", n.ID, "error", err.Error())
		}
		return
	}

	attempt := n.DispatchAttempts + 1
	delay := c.cfg.PushRetryBaseDelay * time.Duration(1<<min(attempt-1, 6))
	c.logger.Warn("push retry scheduled", "notification_id", n.ID, "attempt", attempt, "retry_at", time.Now().Add(delay).String())
	if err := c.queue.MarkDispatchFailed(ctx, n.ID, attempt, time.Now().Add(delay)); err != nil {
		c.logger.Error("mark notification retry", "notification_id", n.ID, "error", err.Error())
	}
}

// sendOne delivers to one device. It returns non-nil only for failures that
// should count toward retry (transient/misc). Invalid tokens prune the device
// and are treated as delivered (never retried).
func (c *PushConsumer) sendOne(ctx context.Context, n *domain.Notification, d *domain.Device) error {
	_, err := c.sender.Send(ctx, push.Message{
		Token: d.Token,
		Title: n.Title,
		Body:  n.Body,
		Data:  stringifyData(n.Data),
	})
	if err == nil {
		c.logger.Info("push sent", "notification_id", n.ID, "device_id", d.ID, "provider", c.sender.Name())
		return nil
	}

	var pErr *push.Error
	if errors.As(err, &pErr) && pErr.IsInvalidToken() {
		c.logger.Info("pruning invalid device token", "notification_id", n.ID, "device_id", d.ID, "error", err.Error())
		if perr := c.devices.DeleteByToken(ctx, d.Token); perr != nil {
			c.logger.Error("prune device token failed", "device_id", d.ID, "error", perr.Error())
		}
		return nil
	}

	c.logger.Warn("push send failed", "notification_id", n.ID, "device_id", d.ID, "retryable", pErr != nil && pErr.Retryable(), "error", err.Error())
	return err
}

// bumpRetry schedules a retry after a non-send failure (e.g. the device query
// failed) so the row is not lost.
func (c *PushConsumer) bumpRetry(ctx context.Context, n *domain.Notification) {
	attempt := n.DispatchAttempts + 1
	delay := c.cfg.PushRetryBaseDelay * time.Duration(1<<min(attempt-1, 6))
	if err := c.queue.MarkDispatchFailed(ctx, n.ID, attempt, time.Now().Add(delay)); err != nil {
		c.logger.Error("mark notification retry (dispatch error)", "notification_id", n.ID, "error", err.Error())
	}
}

func (c *PushConsumer) processReminders(ctx context.Context) error {
	items, err := c.reminders.ClaimDue(ctx, c.cfg.PushBatchSize, 5*time.Minute)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	c.logger.Debug("claimed due reminders", "count", len(items))
	for _, rem := range items {
		c.dispatchReminder(ctx, rem)
	}
	return nil
}

// dispatchReminder turns a due reminder into an inbox notification (which the
// notification loop then pushes) and marks the reminder sent. Failures mark
// the reminder failed rather than retrying forever.
func (c *PushConsumer) dispatchReminder(ctx context.Context, rem *domain.Reminder) {
	ev, err := c.events.GetByID(ctx, rem.EventID)
	if err != nil {
		c.logger.Error("reminder event lookup failed", "reminder_id", rem.ID, "error", err.Error())
		if merr := c.reminders.MarkReminderFailed(ctx, rem.ID); merr != nil {
			c.logger.Error("mark reminder failed", "reminder_id", rem.ID, "error", merr.Error())
		}
		return
	}

	id, err := c.queue.Notify(ctx, rem.UserID, "event_reminder", ev.Title, "This event is starting soon.", map[string]any{"event_id": rem.EventID})
	if err != nil {
		c.logger.Error("reminder notification create failed", "reminder_id", rem.ID, "error", err.Error())
		if merr := c.reminders.MarkReminderFailed(ctx, rem.ID); merr != nil {
			c.logger.Error("mark reminder failed", "reminder_id", rem.ID, "error", merr.Error())
		}
		return
	}

	if err := c.reminders.MarkReminderSent(ctx, rem.ID, id); err != nil {
		c.logger.Error("mark reminder sent", "reminder_id", rem.ID, "error", err.Error())
	}
}

// stringifyData converts the jsonb notification data into the string map the
// push payload accepts.
func stringifyData(data map[string]any) map[string]string {
	if len(data) == 0 {
		return nil
	}
	out := make(map[string]string, len(data))
	for k, v := range data {
		out[k] = fmt.Sprintf("%v", v)
	}
	return out
}
