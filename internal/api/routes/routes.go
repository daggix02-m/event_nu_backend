package routes

import (
	"net/http"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/api"
	"github.com/daggix02-m/event_nu_backend/internal/api/handlers"
	"github.com/daggix02-m/event_nu_backend/internal/api/metrics"
	"github.com/daggix02-m/event_nu_backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/storage"
	"github.com/go-chi/chi/v5"
)

func New(app *api.Application) http.Handler {
	allowed := make(map[string]struct{}, len(app.Config.CORSAllowedOrigins))
	for _, o := range app.Config.CORSAllowedOrigins {
		allowed[o] = struct{}{}
	}

	mux := chi.NewRouter()

	mux.Use(metrics.Count(app.Metrics))
	mux.Use(middleware.RecoverPanic(app.Logger))
	mux.Use(middleware.RequestTimeout(15 * time.Second))
	mux.Use(middleware.CORS(allowed))
	mux.Use(middleware.SecureHeaders)
	mux.Use(middleware.RequestID)
	mux.Use(middleware.LogRequest(app.Logger))
	mux.Use(middleware.BeginRequestTx(app.DB, app.Logger))

	mux.Method(http.MethodGet, "/healthz", handlers.Healthz(app))
	mux.Method(http.MethodGet, "/readyz", handlers.Readyz(app))
	// /metrics is deliberately outside auth/RLS and never touches the database.
	mux.Method(http.MethodGet, "/metrics", handlers.Metrics(app))

	auth := handlers.NewAuthHandlers(app, app.Auth)
	email := handlers.NewEmailHandlers(app)
	org := handlers.NewOrganizerHandlers(app, app.Org)
	events := handlers.NewEventHandlers(app, app.Event, app.Like, app.Save)
	comments := handlers.NewCommentHandlers(app, app.Comment)
	saves := handlers.NewSaveHandlers(app, app.Save)
	shares := handlers.NewShareHandlers(app, app.Share)
	follows := handlers.NewFollowHandlers(app, app.Follow)
	rsvp := handlers.NewRsvpHandlers(app, app.Rsvp)
	reviews := handlers.NewReviewHandlers(app, app.Review)
	reports := handlers.NewReportHandlers(app, app.Report)
	notes := handlers.NewNotificationHandlers(app, app.Notify)
	reminders := handlers.NewReminderHandlers(app, app.Remind)
	ven := handlers.NewVenueHandlers(app, app.Venue)
	adminHandlers := handlers.NewAdminHandlers(app, app.Admin)
	mediaHandle := handlers.NewMediaHandlers(app, app.Media)
	orders := handlers.NewOrderHandlers(app, app.Order, app.Payment)
	payments := handlers.NewPaymentHandlers(app, app.Payment)
	syncHandlers := handlers.NewSyncHandlers(app, app.Sync)
	deviceHandlers := handlers.NewDeviceHandlers(app, app.Device)
	momentHandlers := handlers.NewMomentHandlers(app, app.Moment)
	schedule := handlers.NewScheduleHandlers(app, app.Schedule)
	questions := handlers.NewQuestionHandlers(app, app.Question)
	badges := handlers.NewBadgeHandlers(app, app.Badge)
	recaps := handlers.NewRecapHandlers(app, app.Recap)
	prefs := handlers.NewPreferencesHandlers(app, app.Prefs)
	analyticsHandlers := handlers.NewAnalyticsHandlers(app, app.Analytics)

	// Public/auth-critical routes run under the trusted 'service' role so RLS
	// allows login/registration (identity does not exist yet at that point).
	// Login/register/refresh/verify are rate-limited first (cheapest check):
	// denied requests never reach the handler.
	service := func(h http.HandlerFunc) http.Handler {
		return middleware.WithRole("service")(middleware.SetRLS(h))
	}
	rateLimited := func(h http.HandlerFunc, checks ...middleware.Check) http.Handler {
		return middleware.RateLimit(app.RateLimiter, checks...)(service(h))
	}
	withIP := func(rule middleware.Rule) []middleware.Check {
		return []middleware.Check{{Rule: rule, Key: middleware.IPKey}}
	}
	withIPAndAccount := func(rule middleware.Rule) []middleware.Check {
		return append(withIP(rule), middleware.Check{Rule: rule, Key: middleware.AccountKey})
	}

	registerRule := middleware.Rule{Limit: app.Config.AuthRegisterLimit, Window: app.Config.AuthRegisterWindow}
	loginRule := middleware.Rule{Limit: app.Config.AuthLoginLimit, Window: app.Config.AuthLoginWindow}
	refreshRule := middleware.Rule{Limit: app.Config.AuthRefreshLimit, Window: app.Config.AuthRefreshWindow}
	verifyRule := middleware.Rule{Limit: app.Config.AuthVerifyLimit, Window: app.Config.AuthVerifyWindow}

	// Analytics ingestion is rate-limited by IP (telemetry is anonymous or
	// self-attributed — no account bucket). Runs under the trusted 'service'
	// role so the INSERT policy can verify user_id against the caller's RLS
	// identity; anonymous batches insert NULL user_id.
	ingestRule := middleware.Rule{Limit: app.Config.AnalyticsIngestRateLimit, Window: app.Config.AnalyticsIngestRateWindow}

	mux.Method(http.MethodPost, "/api/v1/auth/register", rateLimited(auth.Register, withIPAndAccount(registerRule)...))
	mux.Method(http.MethodPost, "/api/v1/auth/login", rateLimited(auth.Login, withIPAndAccount(loginRule)...))
	mux.Method(http.MethodPost, "/api/v1/auth/refresh", rateLimited(auth.Refresh, withIP(refreshRule)...))
	mux.Method(http.MethodPost, "/api/v1/auth/logout", service(auth.Logout))
	mux.Method(http.MethodPost, "/api/v1/auth/verify", rateLimited(email.VerifyPOST, withIP(verifyRule)...))

	ingest := func(h http.HandlerFunc) http.Handler {
		return middleware.RateLimit(app.RateLimiter, withIP(ingestRule)...)(
			middleware.OptionalAuth(app.Auth)(
				middleware.WithRole("service")(
					middleware.SetRLS(h))))
	}
	mux.Method(http.MethodPost, "/api/v1/analytics/events", ingest(analyticsHandlers.Ingest))

	// Public read routes: role 'user' with no identity — RLS gates visibility
	// purely on row content (published/active/non-deleted).
	public := func(h http.HandlerFunc) http.Handler {
		return middleware.WithRole("user")(middleware.SetRLS(h))
	}
	mux.Method(http.MethodGet, "/api/v1/venues", public(events.ListVenues))
	mux.Method(http.MethodGet, "/api/v1/categories", public(events.ListCategories))

	optional := func(h http.HandlerFunc) http.Handler {
		return middleware.OptionalAuth(app.Auth)(middleware.SetRLS(h))
	}
	mux.Method(http.MethodGet, "/api/v1/events", optional(events.ListEvents))
	mux.Method(http.MethodGet, "/api/v1/events/{id}", optional(events.GetEvent))
	mux.Method(http.MethodGet, "/api/v1/events/{id}/comments", public(comments.List))
	mux.Method(http.MethodGet, "/api/v1/events/{id}/reviews", public(reviews.List))
	mux.Method(http.MethodGet, "/api/v1/venues/{id}", public(ven.Get))

	mux.Method(http.MethodGet, "/api/v1/organizers/{id}", optional(follows.GetOrganizer))

	// Protected routes: authenticate, then apply RLS as the authenticated user.
	protected := func(h http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(app.Auth)(middleware.SetRLS(h))
	}
	// Client-retryable writes: authenticate, apply RLS, then record the
	// Idempotency-Key (spec §22). Runs inside BeginRequestTx — the stored record
	// shares the request transaction, so a failed write rolls the record back.
	protectedIdem := func(h http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(app.Auth)(
			middleware.SetRLS(
				middleware.Idempotency(app.Idempotency)(h)))
	}
	mux.Method(http.MethodGet, "/api/v1/users/me", protected(auth.Me))
	mux.Method(http.MethodPatch, "/api/v1/users/me", protected(auth.UpdateMe))
	// Onboarding & preferences (Phase 33): GET the user's interest categories,
	// PUT a full replace (which completes onboarding), or explicitly skip.
	// Skip is idempotent and needs no body, so both stay retry-safe without an
	// Idempotency-Key.
	mux.Method(http.MethodGet, "/api/v1/users/me/preferences", protected(prefs.Get))
	mux.Method(http.MethodPut, "/api/v1/users/me/preferences", protected(prefs.Replace))
	mux.Method(http.MethodPost, "/api/v1/users/me/onboarding/skip", protected(prefs.SkipOnboarding))
	mux.Method(http.MethodPost, "/api/v1/organizer-applications", protectedIdem(org.Apply))
	mux.Method(http.MethodGet, "/api/v1/organizer-applications/me", protected(org.GetMyApplication))
	mux.Method(http.MethodGet, "/api/v1/me/organizer", protected(org.GetMyOrganizer))
	mux.Method(http.MethodGet, "/api/v1/me/events", protected(events.ListMyEvents))
	mux.Method(http.MethodPost, "/api/v1/venues", protectedIdem(events.CreateVenue))
	mux.Method(http.MethodPost, "/api/v1/events", protectedIdem(events.CreateEvent))
	mux.Method(http.MethodPatch, "/api/v1/events/{id}", protected(events.UpdateEvent))
	mux.Method(http.MethodPost, "/api/v1/events/{id}/publish", protected(events.Publish))
	mux.Method(http.MethodPost, "/api/v1/events/{id}/comments", protected(comments.Create))
	mux.Method(http.MethodPatch, "/api/v1/comments/{id}", protected(comments.Update))
	mux.Method(http.MethodDelete, "/api/v1/comments/{id}", protected(comments.Delete))
	mux.Method(http.MethodPost, "/api/v1/events/{id}/like", protected(events.AddLike))
	mux.Method(http.MethodDelete, "/api/v1/events/{id}/like", protected(events.RemoveLike))
	mux.Method(http.MethodGet, "/api/v1/me/save-folders", protected(saves.ListFolders))
	mux.Method(http.MethodPost, "/api/v1/me/save-folders", protectedIdem(saves.CreateFolder))
	mux.Method(http.MethodPatch, "/api/v1/me/save-folders/{id}", protected(saves.UpdateFolder))
	mux.Method(http.MethodDelete, "/api/v1/me/save-folders/{id}", protected(saves.DeleteFolder))
	mux.Method(http.MethodGet, "/api/v1/me/saves", protected(saves.ListSaves))
	mux.Method(http.MethodPost, "/api/v1/events/{id}/save", protected(saves.SaveEvent))
	mux.Method(http.MethodDelete, "/api/v1/events/{id}/save", protected(saves.UnsaveEvent))
	mux.Method(http.MethodPost, "/api/v1/events/{id}/share", protectedIdem(shares.RecordShare))
	mux.Method(http.MethodPost, "/api/v1/organizers/{id}/follow", protected(follows.Follow))
	mux.Method(http.MethodDelete, "/api/v1/organizers/{id}/follow", protected(follows.Unfollow))
	mux.Method(http.MethodPost, "/api/v1/events/{id}/rsvp", protected(rsvp.Create))
	mux.Method(http.MethodDelete, "/api/v1/events/{id}/rsvp", protected(rsvp.Cancel))
	mux.Method(http.MethodGet, "/api/v1/events/{id}/rsvp", protected(rsvp.State))
	mux.Method(http.MethodGet, "/api/v1/me/rsvps", protected(rsvp.MyRsvps))
	mux.Method(http.MethodPost, "/api/v1/events/{id}/reviews", protected(reviews.Create))
	mux.Method(http.MethodPatch, "/api/v1/reviews/{id}", protected(reviews.Update))
	mux.Method(http.MethodDelete, "/api/v1/reviews/{id}", protected(reviews.Delete))

	mux.Method(http.MethodPost, "/api/v1/events/{id}/report", protected(reports.ReportTarget("event")))
	mux.Method(http.MethodPost, "/api/v1/users/{id}/report", protected(reports.ReportTarget("user")))
	mux.Method(http.MethodPost, "/api/v1/venues/{id}/report", protected(reports.ReportTarget("venue")))
	mux.Method(http.MethodPost, "/api/v1/comments/{id}/report", protected(reports.ReportTarget("comment")))

	mux.Method(http.MethodGet, "/api/v1/notifications", protected(notes.List))
	mux.Method(http.MethodPost, "/api/v1/notifications/{id}/read", protected(notes.Read))
	mux.Method(http.MethodPost, "/api/v1/notifications/read-all", protected(notes.ReadAll))

	mux.Method(http.MethodPost, "/api/v1/events/{id}/reminders", protected(reminders.Create))
	mux.Method(http.MethodDelete, "/api/v1/events/{id}/reminders", protected(reminders.Delete))
	mux.Method(http.MethodGet, "/api/v1/me/reminders", protected(reminders.My))

	mux.Method(http.MethodPatch, "/api/v1/venues/{id}", protected(ven.Update))
	// Media uploads: intents create assets + presigned URLs (idempotent so a
	// retried intent doesn't duplicate assets); complete enqueues the worker job.
	mux.Method(http.MethodPost, "/api/v1/media/upload-intents", protectedIdem(mediaHandle.CreateUploadIntent))
	mux.Method(http.MethodPost, "/api/v1/media/{id}/complete", protected(mediaHandle.CompleteUpload))
	// Media reads are public (ready assets only, via RLS); deletes are owner-only.
	mux.Method(http.MethodGet, "/api/v1/media/{id}", optional(mediaHandle.GetMedia))
	mux.Method(http.MethodDelete, "/api/v1/media/{id}", protected(mediaHandle.DeleteMedia))

	mux.Method(http.MethodPost, "/api/v1/events/{id}/ticket-types", protectedIdem(orders.CreateTicketType))
	mux.Method(http.MethodGet, "/api/v1/events/{id}/ticket-types", public(orders.ListTicketTypesPublic))
	mux.Method(http.MethodGet, "/api/v1/events/{id}/ticket-types/manage", protected(orders.ListTicketTypes))
	mux.Method(http.MethodPatch, "/api/v1/events/{id}/ticket-types/{ticketTypeID}", protected(orders.UpdateTicketType))
	mux.Method(http.MethodPost, "/api/v1/events/{id}/orders", protectedIdem(orders.CreateOrder))
	mux.Method(http.MethodGet, "/api/v1/orders/{id}", protected(orders.GetOrder))
	mux.Method(http.MethodGet, "/api/v1/me/orders", protected(orders.MyOrders))
	mux.Method(http.MethodPost, "/api/v1/orders/{id}/cancel", protected(orders.CancelOrder))
	mux.Method(http.MethodGet, "/api/v1/me/tickets", protected(orders.MyTickets))
	mux.Method(http.MethodPost, "/api/v1/events/{id}/check-in", protected(orders.CheckIn))

	mux.Method(http.MethodPost, "/webhooks/payments/chapa", service(payments.ChapaWebhook))

	mux.Method(http.MethodGet, "/api/v1/sync", protected(syncHandlers.Pull))
	// Push device registry (Phase 17): register/deregister the caller's FCM
	// tokens. Registration is idempotent (token upsert), so it's retry-safe.
	mux.Method(http.MethodPost, "/api/v1/devices", protectedIdem(deviceHandlers.Register))
	mux.Method(http.MethodGet, "/api/v1/me/devices", protected(deviceHandlers.List))
	mux.Method(http.MethodDelete, "/api/v1/devices/{id}", protected(deviceHandlers.Deregister))

	mux.Method(http.MethodPost, "/api/v1/events/{id}/moments", protectedIdem(momentHandlers.Create))
	mux.Method(http.MethodGet, "/api/v1/events/{id}/moments", public(momentHandlers.List))
	mux.Method(http.MethodDelete, "/api/v1/moments/{id}", protected(momentHandlers.Delete))
	mux.Method(http.MethodGet, "/api/v1/events/{id}/attendees", public(momentHandlers.ListAttendees))

	mux.Method(http.MethodPost, "/api/v1/events/{id}/schedule", protectedIdem(schedule.Create))
	mux.Method(http.MethodGet, "/api/v1/events/{id}/schedule", public(schedule.List))
	mux.Method(http.MethodPatch, "/api/v1/schedule/{id}", protected(schedule.Update))
	mux.Method(http.MethodDelete, "/api/v1/schedule/{id}", protected(schedule.Delete))

	mux.Method(http.MethodPost, "/api/v1/events/{id}/questions", protected(questions.Create))
	mux.Method(http.MethodGet, "/api/v1/events/{id}/questions", optional(questions.List))
	mux.Method(http.MethodPost, "/api/v1/questions/{id}/upvote", protected(questions.Upvote))
	mux.Method(http.MethodDelete, "/api/v1/questions/{id}/upvote", protected(questions.RemoveUpvote))
	mux.Method(http.MethodPost, "/api/v1/questions/{id}/pin", protected(questions.Pin))
	mux.Method(http.MethodPost, "/api/v1/questions/{id}/answer", protected(questions.Answer))

	mux.Method(http.MethodGet, "/api/v1/users/me/badges", protected(badges.MyBadges))

	mux.Method(http.MethodGet, "/api/v1/events/{id}/recap", public(recaps.Get))

	// Local object provider: serve/persist raw objects for the dev/test blob
	// URLs handed out as upload_url / cdn_url. Dev-only, no auth, no RLS.
	if app.Config.MediaStorageProvider == "local" {
		if local, ok := app.Storage.(*storage.Local); ok {
			mux.Method(http.MethodGet, "/media/objects/*", http.HandlerFunc(local.ServeObject))
			mux.Method(http.MethodPut, "/media/objects/*", local.StoreObject(app.Config.MediaMaxUploadBytes))
		}
	}

	admin := func(h http.HandlerFunc) http.Handler {
		return middleware.RequireAdmin(app.Auth)(middleware.WithRole("admin")(middleware.SetRLS(h)))
	}
	mux.Method(http.MethodPost, "/api/v1/admin/organizer-applications/{id}/approve", admin(org.Approve))
	mux.Method(http.MethodPost, "/api/v1/admin/organizer-applications/{id}/reject", admin(org.Reject))
	mux.Method(http.MethodGet, "/api/v1/admin/organizer-applications", admin(adminHandlers.ListApplications))
	mux.Method(http.MethodGet, "/api/v1/admin/reports", admin(adminHandlers.ListReports))
	mux.Method(http.MethodGet, "/api/v1/admin/events", admin(adminHandlers.ListEvents))
	mux.Method(http.MethodPost, "/api/v1/admin/reports/{id}/resolve", admin(adminHandlers.ResolveReport))
	mux.Method(http.MethodPost, "/api/v1/admin/events/{id}/block", admin(adminHandlers.BlockEvent))
	mux.Method(http.MethodPost, "/api/v1/admin/events/{id}/restore", admin(adminHandlers.RestoreEvent))

	mux.Method(http.MethodGet, "/api/v1/admin/organizers", admin(adminHandlers.ListOrganizers))
	mux.Method(http.MethodGet, "/api/v1/admin/organizers/{id}", admin(adminHandlers.GetOrganizer))
	mux.Method(http.MethodPost, "/api/v1/admin/organizers", admin(adminHandlers.CreateOrganizer))

	mux.Method(http.MethodGet, "/api/v1/admin/users", admin(adminHandlers.ListUsers))
	mux.Method(http.MethodPatch, "/api/v1/admin/users/{id}", admin(adminHandlers.UpdateUser))
	mux.Method(http.MethodPost, "/api/v1/admin/users/{id}/suspend", admin(adminHandlers.SuspendUser))
	mux.Method(http.MethodPost, "/api/v1/admin/users/{id}/restore", admin(adminHandlers.RestoreUser))

	mux.Method(http.MethodGet, "/api/v1/admin/venues", admin(adminHandlers.ListVenues))
	mux.Method(http.MethodPost, "/api/v1/admin/venues", admin(adminHandlers.CreateVenue))

	mux.Method(http.MethodGet, "/api/v1/admin/events/{id}", admin(adminHandlers.GetEvent))
	mux.Method(http.MethodPost, "/api/v1/admin/events", admin(adminHandlers.CreateEvent))
	mux.Method(http.MethodPatch, "/api/v1/admin/events/{id}/organizer", admin(adminHandlers.ReassignOrganizer))
	mux.Method(http.MethodPatch, "/api/v1/admin/events/{id}", admin(adminHandlers.UpdateEvent))
	mux.Method(http.MethodPost, "/api/v1/admin/events/{id}/publish", admin(adminHandlers.PublishEvent))
	mux.Method(http.MethodPost, "/api/v1/admin/events/{id}/cancel", admin(adminHandlers.CancelEvent))
	// Admin analytics (Phase 33): dashboards read the rollup table only. Raw
	// events stay worker-private (RLS), so these endpoints can never leak raw
	// per-user behavior.
	mux.Method(http.MethodGet, "/api/v1/admin/analytics/feature-usage", admin(analyticsHandlers.Usage))
	mux.Method(http.MethodGet, "/api/v1/admin/analytics/funnels/event-creation", admin(analyticsHandlers.FunnelEventCreation))
	mux.Method(http.MethodGet, "/api/v1/admin/analytics/funnels/organizer-application", admin(analyticsHandlers.FunnelOrganizerApplication))
	mux.Method(http.MethodGet, "/api/v1/admin/analytics/onboarding", admin(analyticsHandlers.Onboarding))
	mux.Method(http.MethodGet, "/api/v1/admin/analytics/daily-metrics", admin(analyticsHandlers.DailyMetrics))

	return mux
}
