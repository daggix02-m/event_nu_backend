package email

import (
	"context"
	"log/slog"
)

// NoopSender discards messages — used for dev and tests so no external calls
// happen. When EMAIL_PROVIDER=noop, the worker still drains the outbox and
// marks rows sent (simulating delivery), which keeps flows exercisable.
type NoopSender struct {
	logger *slog.Logger
}

func NewNoopSender(logger *slog.Logger) *NoopSender {
	return &NoopSender{logger: logger}
}

func (n *NoopSender) Send(ctx context.Context, m Message) (string, error) {
	if n.logger != nil {
		n.logger.Debug("noop email send", "to", m.RecipientEmail, "template_id", m.TemplateID)
	}
	return "noop-message-id", nil
}