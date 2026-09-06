package api

import (
	"log/slog"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/config"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testConfig() config.Config {
	return config.Config{
		AppEnv:              "test",
		Port:                "8080",
		JWTSecret:           "test-secret",
		JWTExpiry:           15 * time.Minute,
		RefreshTokenExpiry:  30 * 24 * time.Hour,
		CORSAllowedOrigins:  []string{"https://example.com"},
		EmailProvider:       "noop",
		EmailPollInterval:   2 * time.Second,
		EmailBatchSize:      20,
		EmailMaxAttempts:    3,
		EmailRetryBaseDelay: 30 * time.Second,
	}
}

func TestNewAppWiresAllServices(t *testing.T) {
	cfg := testConfig()
	app := NewApp(testLogger(), cfg, nil)

	if app.Auth == nil {
		t.Fatal("expected Auth service to be wired")
	}
	if app.Email == nil {
		t.Fatal("expected Email service to be wired")
	}
	if app.Org == nil {
		t.Fatal("expected Org service to be wired")
	}
	if app.Event == nil {
		t.Fatal("expected Event service to be wired")
	}
}

func TestNewAppCarriesConfigLoggerAndPool(t *testing.T) {
	cfg := testConfig()
	logger := testLogger()
	app := NewApp(logger, cfg, nil)

	if app.DB != nil {
		t.Fatalf("expected pool to be carried through untouched")
	}
	if !reflect.DeepEqual(app.Config, cfg) {
		t.Fatalf("config mismatch:\n got %+v\nwant %+v", app.Config, cfg)
	}
	if app.Logger != logger {
		t.Fatalf("expected the same logger instance")
	}
}

func TestNewAppTwoInstancesAreIndependent(t *testing.T) {
	a := NewApp(testLogger(), testConfig(), nil)
	b := NewApp(testLogger(), testConfig(), nil)

	if a.Auth == b.Auth {
		t.Fatal("expected separate service instances per Application")
	}
	if a.Event == b.Event {
		t.Fatal("expected separate service instances per Application")
	}
}
