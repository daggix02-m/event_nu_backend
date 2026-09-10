package api

import (
	"log/slog"

	"github.com/daggix02-m/event_nu/backend/internal/api/metrics"
	"github.com/daggix02-m/event_nu/backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/storage"
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

	// RateLimiter is the shared in-memory fixed-window limiter for the auth
	// endpoints. Created per app instance so tests get isolated state.
	RateLimiter *middleware.Limiter

	Auth    *service.AuthService
	Email   *service.EmailService
	Org     *service.OrganizerService
	Event   *service.EventService
	Comment *service.CommentService
	Like    *service.LikeService
	Save    *service.SaveService
	Share   *service.ShareService
	Follow  *service.FollowService
	Rsvp    *service.RsvpService
	Review  *service.ReviewService
	Report  *service.ReportService
	Notify  *service.NotificationService
	Remind  *service.ReminderService
	Venue   *service.VenueService
	Admin   *service.AdminService
	Media   *service.MediaService

	// Storage backs the media pipeline (local files for dev/tests, S3/R2 for
	// production). Exposed so handlers/workers can reach it when needed.
	Storage storage.ObjectStore

	// Idempotency backs the client-retryable write middleware. Repositories
	// read the request tx from context, so records share the guarded write's
	// transaction (a rollback undoes the record too).
	Idempotency *repository.IdempotencyRepository

	// Metrics backs the /metrics endpoint (process gauges + HTTP request
	// counter). Created per app instance so tests get isolated state.
	Metrics *metrics.Registry
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
	comments := repository.NewCommentRepository(pool)
	likes := repository.NewLikeRepository(pool)
	saves := repository.NewSaveRepository(pool)
	shares := repository.NewShareRepository(pool)
	follows := repository.NewFollowRepository(pool)
	rsvps := repository.NewRsvpRepository(pool)
	reviews := repository.NewReviewRepository(pool)
	reports := repository.NewReportRepository(pool)
	notifier := repository.NewNotificationRepository(pool)
	reminders := repository.NewReminderRepository(pool)

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
		panic("storage init: " + err.Error())
	}
	media := service.NewMediaService(repository.NewMediaRepository(pool), store, cfg)

	app := &Application{
		Logger:      logger,
		Config:      cfg,
		DB:          pool,
		RateLimiter: middleware.NewLimiter(),
		Auth:        service.NewAuthService(users, sessions, cfg),
		Email:       service.NewEmailService(outbox, codes, users, cfg),
		Org:         service.NewOrganizerService(orgs),
		Event:       service.NewEventService(events, venues, cats, orgs),
		Comment:     service.NewCommentService(comments, events),
		Like:        service.NewLikeService(likes, events),
		Save:        service.NewSaveService(saves, events),
		Share:       service.NewShareService(shares, events),
		Follow:      service.NewFollowService(follows, orgs),
		Rsvp:        service.NewRsvpService(rsvps, events),
		Review:      service.NewReviewService(reviews, rsvps, events),
		Report:      service.NewReportService(reports, events, venues, comments),
		Notify:      service.NewNotificationService(notifier, events, orgs),
		Remind:      service.NewReminderService(reminders, events),
		Venue:       service.NewVenueService(venues, events, orgs),
		Admin:       service.NewAdminService(reports, events),
		Media:       media,
		Storage:     store,
		Idempotency: repository.NewIdempotencyRepository(pool),
		Metrics:     metrics.NewRegistry(),
	}

	// Notification fan-out hooks: every create that should notify someone runs
	// through these nil-safe notifiers (they are no-ops when not wired).
	app.Comment.SetNotifier(app.Notify)
	app.Rsvp.SetNotifier(app.Notify)
	app.Follow.SetNotifier(app.Notify)
	// Event media attachment validation (owned, ready, right kind).
	app.Event.SetMediaStore(media)

	return app
}
