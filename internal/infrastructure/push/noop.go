package push

import (
	"context"
	"log/slog"
)

// NoopProvider discards messages — used for dev and tests so no external calls
// happen. The worker still drains the notifications table and marks rows
// dispatched, which keeps the flows exercisable.
type NoopProvider struct {
	logger *slog.Logger
}

func NewNoopProvider(logger *slog.Logger) *NoopProvider {
	return &NoopProvider{logger: logger}
}

func (p *NoopProvider) Name() string { return "noop" }

func (p *NoopProvider) Send(_ context.Context, m Message) (string, error) {
	if p.logger != nil {
		p.logger.Debug("noop push send", "token", m.Token, "title", m.Title)
	}
	return "noop-message-id", nil
}

var _ Provider = (*NoopProvider)(nil)
