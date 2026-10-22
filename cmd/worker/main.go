package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/config"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/email"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/kafka"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/push"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/storage"
	"github.com/daggix02-m/event_nu_backend/internal/repository"
	"github.com/daggix02-m/event_nu_backend/internal/worker"
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

	// Domain-event relay + push fanout: only when Kafka is part of the
	// deployment. The relay owns the broker's write side — the API writes
	// facts to the outbox atomically, this process ships them (at-least-once
	// via lease + retry). The fanout is the read side: a consumer group that
	// turns event.published into follower push notifications.
	var relay *worker.DomainEventRelay
	var fanout *worker.EventPublishedFanout
	if len(cfg.KafkaBrokers) > 0 {
		producer := kafka.NewProducer(kafka.ProducerConfig{
			Brokers:         cfg.KafkaBrokers,
			Topic:           cfg.KafkaTopicEvents,
			DeliveryTimeout: 10 * time.Second,
		}, logger)
		defer producer.Close()
		relay = worker.NewDomainEventRelay(logger, cfg, repository.NewDomainEventRepository(pool), producer)
		if err := kafka.EnsureTopic(ctx, cfg.KafkaBrokers, cfg.KafkaTopicEvents, logger); err != nil {
			logger.Error("ensuring kafka topic failed", "error", err.Error())
			os.Exit(1)
		}
		logger.Info("domain event relay enabled", "topic", cfg.KafkaTopicEvents)
	} else {
		logger.Info("domain event relay disabled (KAFKA_BROKERS unset)")
	}

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

	// The fanout reads the same RLS-privileged service-role pool; it shares
	// the push provider the reminder dispatcher uses.
	if len(cfg.KafkaBrokers) > 0 {
		fanout = worker.NewEventPublishedFanout(logger, cfg,
			repository.NewFollowRepository(pool),
			repository.NewDeviceRepository(pool),
			repository.NewDeviceRepository(pool),
			repository.NewEventRepository(pool),
			pusher,
		)
		logger.Info("event published fanout enabled", "group", cfg.KafkaPushGroup)
	} else {
		logger.Info("event published fanout disabled (KAFKA_BROKERS unset)")
	}

	analyticsConsumer := worker.NewAnalyticsRollupConsumer(
		logger,
		cfg,
		repository.NewAnalyticsRepository(pool),
	)

	logger.Info("worker starting", "email_provider", cfg.EmailProvider, "storage_provider", cfg.MediaStorageProvider, "push_provider", cfg.PushProvider)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return emailConsumer.Run(gctx) })
	g.Go(func() error { return mediaConsumer.Run(gctx) })
	g.Go(func() error { return pushConsumer.Run(gctx) })
	g.Go(func() error { return analyticsConsumer.Run(gctx) })
	if relay != nil {
		g.Go(func() error { return relay.Run(gctx) })
	}
	if fanout != nil {
		g.Go(func() error { return fanout.Run(gctx) })
	}
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
