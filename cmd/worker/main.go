package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/email"
	"github.com/daggix02-m/event_nu/backend/internal/repository"
	"github.com/daggix02-m/event_nu/backend/internal/worker"
	"github.com/joho/godotenv"
)

// The worker runs background consumers (email outbox now; media and jobs
// later). It shares config and the database with the API.
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

	var sender email.Sender
	switch cfg.EmailProvider {
	case "brevo":
		sender = email.NewBrevoClient(cfg.BrevoAPIKey, cfg.BrevoAPIBase, cfg.BrevoSenderName, cfg.BrevoAPISender, cfg.BrevoSenderEmail)
	default:
		sender = email.NewNoopSender(logger)
	}

	outbox := repository.NewOutboxRepository(pool)
	consumer := worker.NewEmailConsumer(logger, cfg, outbox, sender)

	logger.Info("worker starting", "email_provider", cfg.EmailProvider)
	if err := consumer.Run(ctx); err != nil {
		logger.Error("consumer error", "error", err.Error())
		os.Exit(1)
	}
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
