package worker

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/config"
)

type orderingRollupStore struct {
	rollupErr error
	log       []string
}

func (s *orderingRollupStore) RollupDay(_ context.Context, _ time.Time) (int, error) {
	s.log = append(s.log, "rollup")
	return 0, s.rollupErr
}

func (s *orderingRollupStore) DeleteBefore(_ context.Context, _, _ time.Time) (int64, error) {
	s.log = append(s.log, "delete")
	return 0, nil
}

func rollupConsumer(store analyticsRollupStore) *AnalyticsRollupConsumer {
	cfg := config.Config{
		AnalyticsRollupPollInterval: time.Minute,
		AnalyticsRetentionDays:      90,
	}
	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	return NewAnalyticsRollupConsumer(logger, cfg, store)
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestAnalyticsRollupRunsBeforeRetention(t *testing.T) {
	store := &orderingRollupStore{}
	c := rollupConsumer(store)
	if err := c.rollupOnce(context.Background()); err != nil {
		t.Fatalf("rollupOnce: %v", err)
	}
	if len(store.log) != 2 || store.log[0] != "rollup" || store.log[1] != "delete" {
		t.Fatalf("expected rollup then delete, got %v", store.log)
	}
}

func TestAnalyticsRetentionSkippedWhenRollupFails(t *testing.T) {
	store := &orderingRollupStore{rollupErr: errors.New("db down")}
	c := rollupConsumer(store)
	if err := c.rollupOnce(context.Background()); err == nil {
		t.Fatal("expected rollup failure to propagate")
	}
	if len(store.log) != 1 || store.log[0] != "rollup" {
		t.Fatalf("retention must be skipped when rollup fails, got %v", store.log)
	}
}
