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

	Auth  *service.AuthService
	Email *service.EmailService
	Org   *service.OrganizerService
	Event *service.EventService
}

func NewApp(logger *slog.Logger, cfg config.Config, pool *pgxpool.Pool) *Application {
	users := repository.NewUserRepository(pool)
	sessions := repository.NewSessionRepository(pool)
	outbox := repository.NewOutboxRepository(pool)
	codes := repository.NewEmailCodeRepository(pool)
	orgs := repository.NewOrganizerRepository(pool)
	venues := repository.NewVenueRepository(pool)
	cats := repository.NewCategoryRepository(pool)
	events := repository.NewEventRepository(pool)

	return &Application{
		Logger: logger,
		Config: cfg,
		DB:     pool,
		Auth:   service.NewAuthService(users, sessions, cfg),
		Email:  service.NewEmailService(outbox, codes, users, cfg),
		Org:    service.NewOrganizerService(orgs),
		Event:  service.NewEventService(events, venues, cats, orgs),
	}
}
