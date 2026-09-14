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
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/push"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/storage"
	"github.com/daggix02-m/event_nu/backend/internal/repository"
	"github.com/daggix02-m/event_nu/backend/internal/worker"
	"github.com/joho/godotenv"
	"golang.org/x/sync/errgroup"
)

// The worker runs background consumers (email outbox, media processing). It
// shares config and the database with the API, connecting as the 'service'
// role so RLS grants read/write on every table.
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
	emailConsumer := worker.NewEmailConsumer(logger, cfg, outbox, sender)

	store, err := storage.New(
		cfg.MediaStorageProvider,
		cfg.MediaS3Endpoint,
		cfg.MediaS3Region,
		cfg.MediaS3Bucket,
		cfg.MediaS3AccessKey,
		cfg.MediaS3SecretKey,
		cfg.MediaPublicBase,
		cfg.MediaLocalDir,
	)
	if err != nil {
		logger.Error("storage init failed", "error", err.Error())
		os.Exit(1)
	}
	mediaConsumer := worker.NewMediaConsumer(logger, cfg, repository.NewMediaRepository(pool), store)

	var pusher push.Provider
	pusher, err = push.New(cfg.PushProvider, push.FCMConfig{
		ProjectID:      cfg.FCMProjectID,
		ServiceAccount: cfg.FCMServiceAccount,
		APIBase:        cfg.FCMAPIBase,
		TokenURL:       cfg.FCMTokenURL,
	})
	if err != nil {
		logger.Error("push provider init failed", "error", err.Error())
		os.Exit(1)
	}
	pushConsumer := worker.NewPushConsumer(
		logger,
		cfg,
		repository.NewNotificationRepository(pool),
		repository.NewReminderRepository(pool),
		repository.NewDeviceRepository(pool),
		repository.NewEventRepository(pool),
		pusher,
	)

	logger.Info("worker starting", "email_provider", cfg.EmailProvider, "storage_provider", cfg.MediaStorageProvider, "push_provider", cfg.PushProvider)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return emailConsumer.Run(gctx) })
	g.Go(func() error { return mediaConsumer.Run(gctx) })
	g.Go(func() error { return pushConsumer.Run(gctx) })
	if err := g.Wait(); err != nil {
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
