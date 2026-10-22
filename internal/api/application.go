package api

import (
	"log/slog"

	"github.com/daggix02-m/event_nu_backend/internal/api/metrics"
	"github.com/daggix02-m/event_nu_backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu_backend/internal/config"
	"github.com/daggix02-m/event_nu_backend/internal/domain"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/payments"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/storage"
	"github.com/daggix02-m/event_nu_backend/internal/repository"
	"github.com/daggix02-m/event_nu_backend/internal/service"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Application struct {
	Logger *slog.Logger
	Config config.Config
	DB     *pgxpool.Pool

	RateLimiter *middleware.Limiter

	Auth      *service.AuthService
	Email     *service.EmailService
	Org       *service.OrganizerService
	Event     *service.EventService
	Comment   *service.CommentService
	Like      *service.LikeService
	Save      *service.SaveService
	Share     *service.ShareService
	Follow    *service.FollowService
	Rsvp      *service.RsvpService
	Review    *service.ReviewService
	Report    *service.ReportService
	Notify    *service.NotificationService
	Remind    *service.ReminderService
	Venue     *service.VenueService
	Admin     *service.AdminService
	Media     *service.MediaService
	Order     *service.OrderService
	Payment   *service.PaymentService
	Sync      *service.SyncService
	Device    *service.DeviceService
	Moment    *service.MomentService
	Schedule  *service.ScheduleService
	Question  *service.QuestionService
	Badge     *service.BadgeService
	Recap     *service.RecapService
	Prefs     *service.PreferencesService
	Analytics *service.AnalyticsService

	Storage domain.ObjectStore

	// Idempotency backs the client-retryable write middleware. Repositories
	// read the request tx from context, so records share the guarded write's
	// transaction (a rollback undoes the record too).
	Idempotency *repository.IdempotencyRepository

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
	deviceRepo := repository.NewDeviceRepository(pool)

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

	questionRepo := repository.NewQuestionRepository(pool)
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
		Admin:       service.NewAdminService(reports, events, orgs, events, orgs, users, venues),
		Media:       media,
		Order:       service.NewOrderService(repository.NewOrderRepository(pool), repository.NewTicketTypeRepository(pool), events, orgs, cfg.TicketQRSecret),
		Storage:     store,
		Idempotency: repository.NewIdempotencyRepository(pool),
		Metrics:     metrics.NewRegistry(),
		Sync:        service.NewSyncService(repository.NewSyncRepository(pool)),
		Device:      service.NewDeviceService(deviceRepo),
		Moment:      service.NewMomentService(repository.NewMomentRepository(pool), events),
		Schedule:    service.NewScheduleService(repository.NewEventSessionRepository(pool), events, orgs),
		Question:    service.NewQuestionService(questionRepo, questionRepo, events, orgs),
		Badge:       service.NewBadgeService(repository.NewBadgeRepository(pool)),
		Recap:       service.NewRecapService(repository.NewRecapRepository(pool), events),
		Prefs:       service.NewPreferencesService(repository.NewCategoryPreferenceRepository(pool), users),
		Analytics:   service.NewAnalyticsService(repository.NewAnalyticsRepository(pool), cfg),
	}

	app.Payment = service.NewPaymentService(
		repository.NewOrderRepository(pool),
		repository.NewPaymentRepository(pool),
		users,
		payments.New(cfg.PaymentProvider, cfg.ChapaAPIBase, cfg.ChapaSecretKey),
		app.Notify,
		cfg,
	)

	app.Comment.SetNotifier(app.Notify)
	app.Rsvp.SetNotifier(app.Notify)
	app.Follow.SetNotifier(app.Notify)

	app.Rsvp.SetBadges(app.Badge)
	app.Review.SetBadges(app.Badge)
	app.Moment.SetBadges(app.Badge)
	app.Order.SetBadges(app.Badge)

	app.Notify.SetRsvpStore(rsvps)

	app.Event.SetMediaStore(media)

	app.Admin.SetMediaStore(media)

	return app
}
