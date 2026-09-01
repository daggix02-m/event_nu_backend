package api

import (
	"log/slog"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/repository"
	"github.com/daggix02-m/event_nu/backend/internal/service"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Application carries every shared dependency. Handlers are methods on it —
// no globals, trivially testable by swapping dependencies.
type Application struct {
	Logger *slog.Logger
	Config config.Config
	DB     *pgxpool.Pool

	Auth *service.AuthService
}

func NewApp(logger *slog.Logger, cfg config.Config, pool *pgxpool.Pool) *Application {
	users := repository.NewUserRepository(pool)
	sessions := repository.NewSessionRepository(pool)

	return &Application{
		Logger: logger,
		Config: cfg,
		DB:     pool,
		Auth:   service.NewAuthService(users, sessions, cfg),
	}
}