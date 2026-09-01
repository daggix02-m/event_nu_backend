package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/joho/godotenv"
)

// The worker runs background consumers (email outbox in Step 5, media and
// jobs later). It shares config and the database with the API.
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

	pool, err := database.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database connection failed", "error", err.Error())
		os.Exit(1)
	}
	defer pool.Close()

	logger.Info("worker starting")
	<-ctx.Done()
	logger.Info("worker stopped")
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