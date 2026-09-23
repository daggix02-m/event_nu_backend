package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/config"
)

// analyticsRollupStore is the persistence surface the rollup consumer needs
// (mirrors the other consumers so tests can stub it).
type analyticsRollupStore interface {
	RollupDay(ctx context.Context, day time.Time) (int, error)
	DeleteBefore(ctx context.Context, cutoff time.Time, utcToday time.Time) (int64, error)
}

// AnalyticsRollupConsumer recomputes the previous UTC day's aggregates from the
// raw event log and then applies retention. Rollup-first ordering matters: it
// never deletes events (current day aside) until the day they fall into has
// been rolled, so deleted days are never rolled again and the aggregates never
// lose data that could have been counted.
type AnalyticsRollupConsumer struct {
	logger *slog.Logger
	cfg    config.Config
	store  analyticsRollupStore
}

func NewAnalyticsRollupConsumer(logger *slog.Logger, cfg config.Config, store analyticsRollupStore) *AnalyticsRollupConsumer {
	return &AnalyticsRollupConsumer{logger: logger, cfg: cfg, store: store}
}

func (c *AnalyticsRollupConsumer) Run(ctx context.Context) error {
	c.logger.Info("analytics rollup consumer started",
		"poll_interval", c.cfg.AnalyticsRollupPollInterval.String(),
		"retention_days", c.cfg.AnalyticsRetentionDays)
	ticker := time.NewTicker(c.cfg.AnalyticsRollupPollInterval)
	defer ticker.Stop()

	for {
		if err := c.rollupOnce(ctx); err != nil && ctx.Err() == nil {
			c.logger.Error("analytics rollup failed", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			c.logger.Info("analytics rollup consumer stopped")
			return nil
		case <-ticker.C:
		}
	}
}

// rollupOnce runs one rollup pass: recompute yesterday, then delete anything
// older than the retention window (never the current UTC day). Retention is
// skipped when the rollup fails, so events for an un-rolled day are kept.
func (c *AnalyticsRollupConsumer) rollupOnce(ctx context.Context) error {
	now := time.Now().UTC()
	utcToday := now.Truncate(24 * time.Hour)
	yesterday := utcToday.Add(-24 * time.Hour)

	rolled, err := c.store.RollupDay(ctx, yesterday)
	if err != nil {
		return fmt.Errorf("rollup %s: %w", yesterday.Format("2006-01-02"), err)
	}
	c.logger.Info("rolled analytics day", "day", yesterday.Format("2006-01-02"), "rows", rolled)

	cutoff := now.Add(-time.Duration(c.cfg.AnalyticsRetentionDays) * 24 * time.Hour)
	deleted, err := c.store.DeleteBefore(ctx, cutoff, utcToday)
	if err != nil {
		return fmt.Errorf("retention: %w", err)
	}
	if deleted > 0 {
		c.logger.Info("analytics retention applied", "deleted", deleted, "cutoff", cutoff.UTC().Format(time.RFC3339))
	}
	return nil
}
