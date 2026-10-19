package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/daggix02-m/event_nu_backend/internal/config"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu_backend/internal/repository"
	"github.com/daggix02-m/event_nu_backend/internal/worker"
	"github.com/joho/godotenv"
)

// The analytics rollup worker recomputes the daily feature-usage aggregates
// and applies retention. It connects as the 'service' role — the only role
// allowed to read raw analytics_events (RLS) — and shares config with the API.
func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err.Error())
		os.Exit(1)
	}

	logger := newLogger(cfg.AppEnv)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := database.NewPoolWithRole(ctx, cfg.DatabaseURL, "service")
	if err != nil {
		logger.Error("database connection failed", "error", err.Error())
		os.Exit(1)
	}
	defer pool.Close()

	consumer := worker.NewAnalyticsRollupConsumer(
		logger,
		cfg,
		repository.NewAnalyticsRepository(pool),
	)

	logger.Info("analytics rollup worker starting",
		"poll_interval", cfg.AnalyticsRollupPollInterval.String(),
		"retention_days", cfg.AnalyticsRetentionDays)
	if err := consumer.Run(ctx); err != nil {
		logger.Error("analytics rollup error", "error", err.Error())
		os.Exit(1)
	}
	logger.Info("analytics rollup worker stopped")
}

func newLogger(env string) *slog.Logger {
	var handler slog.Handler
	if env == "development" {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	} else {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	}
	return slog.New(handler)
}
