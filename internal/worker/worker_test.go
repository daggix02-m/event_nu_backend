package worker

import (
	"log/slog"
	"os"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/config"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testWorkerConfig() config.Config {
	return config.Config{
		EmailPollInterval:   time.Second,
		EmailBatchSize:      20,
		EmailMaxAttempts:    3,
		EmailRetryBaseDelay: 30 * time.Second,
	}
}