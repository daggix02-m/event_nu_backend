package routes

import (
	"net/http"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/handlers"
	"github.com/daggix02-m/event_nu/backend/internal/api/metrics"
	"github.com/daggix02-m/event_nu/backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/storage"
)

// New builds the route table and wraps it in the middleware chain.
func New(app *api.Application) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handlers.Healthz(app))
	mux.HandleFunc("GET /readyz", handlers.Readyz(app))
	// /metrics is deliberately outside auth/RLS and never touches the database.
	mux.HandleFunc("GET /metrics", handlers.Metrics(app))

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

	mux.Handle("POST /api/v1/auth/register", rateLimited(auth.Register, withIPAndAccount(registerRule)...))
	mux.Handle("POST /api/v1/auth/login", rateLimited(auth.Login, withIPAndAccount(loginRule)...))
	mux.Handle("POST /api/v1/auth/refresh", rateLimited(auth.Refresh, withIP(refreshRule)...))
	mux.Handle("POST /api/v1/auth/logout", service(auth.Logout))
	mux.Handle("POST /api/v1/auth/verify", rateLimited(email.VerifyPOST, withIP(verifyRule)...))

	// Public read routes: role 'user' with no identity — RLS gates visibility
	// purely on row content (published/active/non-deleted).
	public := func(h http.HandlerFunc) http.Handler {
		return middleware.WithRole("user")(middleware.SetRLS(h))
	}
	mux.Handle("GET /api/v1/venues", public(events.ListVenues))
	mux.Handle("GET /api/v1/categories", public(events.ListCategories))
	// Event reads surface per-user like state, so they run with optional
	// authentication: a valid Bearer token personalizes the response; missing
	// or invalid tokens degrade to the anonymous view rather than 401.
	optional := func(h http.HandlerFunc) http.Handler {
		return middleware.OptionalAuth(app.Auth)(middleware.SetRLS(h))
	}
	mux.Handle("GET /api/v1/events", optional(events.ListEvents))
	mux.Handle("GET /api/v1/events/{id}", optional(events.GetEvent))
	mux.Handle("GET /api/v1/events/{id}/comments", public(comments.List))
	mux.Handle("GET /api/v1/events/{id}/reviews", public(reviews.List))
	mux.Handle("GET /api/v1/venues/{id}", public(ven.Get))
	// Organizer profiles need follow-state personalization, so they also run
	// under optional auth (anonymous readers get followed_by_me=false).
	mux.Handle("GET /api/v1/organizers/{id}", optional(follows.GetOrganizer))

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
	mux.Handle("GET /api/v1/users/me", protected(auth.Me))
	mux.Handle("PATCH /api/v1/users/me", protected(auth.UpdateMe))
	mux.Handle("POST /api/v1/organizer-applications", protectedIdem(org.Apply))
	mux.Handle("GET /api/v1/organizer-applications/me", protected(org.GetMyApplication))
	mux.Handle("POST /api/v1/venues", protectedIdem(events.CreateVenue))
	mux.Handle("POST /api/v1/events", protectedIdem(events.CreateEvent))
	mux.Handle("PATCH /api/v1/events/{id}", protected(events.UpdateEvent))
	mux.Handle("POST /api/v1/events/{id}/publish", protected(events.Publish))
	mux.Handle("POST /api/v1/events/{id}/comments", protected(comments.Create))
	mux.Handle("PATCH /api/v1/comments/{id}", protected(comments.Update))
	mux.Handle("DELETE /api/v1/comments/{id}", protected(comments.Delete))
	mux.Handle("POST /api/v1/events/{id}/like", protected(events.AddLike))
	mux.Handle("DELETE /api/v1/events/{id}/like", protected(events.RemoveLike))
	mux.Handle("GET /api/v1/me/save-folders", protected(saves.ListFolders))
	mux.Handle("POST /api/v1/me/save-folders", protectedIdem(saves.CreateFolder))
	mux.Handle("PATCH /api/v1/me/save-folders/{id}", protected(saves.UpdateFolder))
	mux.Handle("DELETE /api/v1/me/save-folders/{id}", protected(saves.DeleteFolder))
	mux.Handle("GET /api/v1/me/saves", protected(saves.ListSaves))
	mux.Handle("POST /api/v1/events/{id}/save", protected(saves.SaveEvent))
	mux.Handle("DELETE /api/v1/events/{id}/save", protected(saves.UnsaveEvent))
	mux.Handle("POST /api/v1/events/{id}/share", protectedIdem(shares.RecordShare))
	mux.Handle("POST /api/v1/organizers/{id}/follow", protected(follows.Follow))
	mux.Handle("DELETE /api/v1/organizers/{id}/follow", protected(follows.Unfollow))
	mux.Handle("POST /api/v1/events/{id}/rsvp", protected(rsvp.Create))
	mux.Handle("DELETE /api/v1/events/{id}/rsvp", protected(rsvp.Cancel))
	mux.Handle("GET /api/v1/events/{id}/rsvp", protected(rsvp.State))
	mux.Handle("GET /api/v1/me/rsvps", protected(rsvp.MyRsvps))
	mux.Handle("POST /api/v1/events/{id}/reviews", protected(reviews.Create))
	mux.Handle("PATCH /api/v1/reviews/{id}", protected(reviews.Update))
	mux.Handle("DELETE /api/v1/reviews/{id}", protected(reviews.Delete))
	// Reports: target id must be a UUID; the entity type is fixed per route.
	mux.Handle("POST /api/v1/events/{id}/report", protected(reports.ReportTarget("event")))
	mux.Handle("POST /api/v1/users/{id}/report", protected(reports.ReportTarget("user")))
	mux.Handle("POST /api/v1/venues/{id}/report", protected(reports.ReportTarget("venue")))
	mux.Handle("POST /api/v1/comments/{id}/report", protected(reports.ReportTarget("comment")))
	// Notification inbox (dispatch arrives with the Phase 17 worker).
	mux.Handle("GET /api/v1/notifications", protected(notes.List))
	mux.Handle("POST /api/v1/notifications/{id}/read", protected(notes.Read))
	mux.Handle("POST /api/v1/notifications/read-all", protected(notes.ReadAll))
	// Event reminders (record-only until the dispatch worker in Phase 17).
	mux.Handle("POST /api/v1/events/{id}/reminders", protected(reminders.Create))
	mux.Handle("DELETE /api/v1/events/{id}/reminders", protected(reminders.Delete))
	mux.Handle("GET /api/v1/me/reminders", protected(reminders.My))
	// Venue editing (organizer-owned).
	mux.Handle("PATCH /api/v1/venues/{id}", protected(ven.Update))
	// Media uploads: intents create assets + presigned URLs (idempotent so a
	// retried intent doesn't duplicate assets); complete enqueues the worker job.
	mux.Handle("POST /api/v1/media/upload-intents", protectedIdem(mediaHandle.CreateUploadIntent))
	mux.Handle("POST /api/v1/media/{id}/complete", protected(mediaHandle.CompleteUpload))
	// Media reads are public (ready assets only, via RLS); deletes are owner-only.
	mux.Handle("GET /api/v1/media/{id}", optional(mediaHandle.GetMedia))
	mux.Handle("DELETE /api/v1/media/{id}", protected(mediaHandle.DeleteMedia))
	// Tickets & orders (Phase 14): tier management (organizer), public tier
	// listing, atomic order reservation, order/ticket wallet, QR check-in.
	mux.Handle("POST /api/v1/events/{id}/ticket-types", protectedIdem(orders.CreateTicketType))
	mux.Handle("GET /api/v1/events/{id}/ticket-types", public(orders.ListTicketTypesPublic))
	mux.Handle("GET /api/v1/events/{id}/ticket-types/manage", protected(orders.ListTicketTypes))
	mux.Handle("PATCH /api/v1/events/{id}/ticket-types/{ticketTypeID}", protected(orders.UpdateTicketType))
	mux.Handle("POST /api/v1/events/{id}/orders", protectedIdem(orders.CreateOrder))
	mux.Handle("GET /api/v1/orders/{id}", protected(orders.GetOrder))
	mux.Handle("GET /api/v1/me/orders", protected(orders.MyOrders))
	mux.Handle("POST /api/v1/orders/{id}/cancel", protected(orders.CancelOrder))
	mux.Handle("GET /api/v1/me/tickets", protected(orders.MyTickets))
	mux.Handle("POST /api/v1/events/{id}/check-in", protected(orders.CheckIn))
	// Payment callback (Phase 15): provider POSTs outcomes here. Runs under the
	// trusted 'service' role inside the request transaction so the order state
	// change commits atomically.
	mux.Handle("POST /webhooks/payments/chapa", service(payments.ChapaWebhook))
	// Offline delta (Phase 16): signed-in users pull the public catalog since a
	// cursor. Per-user rows stay out of sync — the client re-fetches them via
	// their own endpoints (private full refresh).
	mux.Handle("GET /api/v1/sync", protected(syncHandlers.Pull))

	// Local object provider: serve/persist raw objects for the dev/test blob
	// URLs handed out as upload_url / cdn_url. Dev-only, no auth, no RLS.
	if app.Config.MediaStorageProvider == "local" {
		if local, ok := app.Storage.(*storage.Local); ok {
			mux.HandleFunc("GET /media/objects/{key...}", local.ServeObject)
			mux.Handle("PUT /media/objects/{key...}", local.StoreObject(app.Config.MediaMaxUploadBytes))
		}
	}

	// Admin business actions stay in Go (they trigger side effects + audits).
	admin := func(h http.HandlerFunc) http.Handler {
		return middleware.RequireAdmin(app.Auth)(middleware.WithRole("admin")(middleware.SetRLS(h)))
	}
	mux.Handle("POST /api/v1/admin/organizer-applications/{id}/approve", admin(org.Approve))
	mux.Handle("POST /api/v1/admin/organizer-applications/{id}/reject", admin(org.Reject))
	mux.Handle("GET /api/v1/admin/reports", admin(adminHandlers.ListReports))
	mux.Handle("POST /api/v1/admin/reports/{id}/resolve", admin(adminHandlers.ResolveReport))
	mux.Handle("POST /api/v1/admin/events/{id}/block", admin(adminHandlers.BlockEvent))
	mux.Handle("POST /api/v1/admin/events/{id}/restore", admin(adminHandlers.RestoreEvent))

	allowed := make(map[string]struct{}, len(app.Config.CORSAllowedOrigins))
	for _, o := range app.Config.CORSAllowedOrigins {
		allowed[o] = struct{}{}
	}

	// Count every completed request (method + status) at the outermost edge —
	// outside RecoverPanic's 500 conversion and CORS short-circuits, but not
	// inside LogRequest's redaction scope. /metrics scrapes are counted too,
	// which is the normal Prometheus self-measurement behavior.
	return metrics.Count(app.Metrics)(
		middleware.RecoverPanic(app.Logger)(
			middleware.RequestTimeout(15 * time.Second)(
				middleware.CORS(allowed)(
					middleware.SecureHeaders(
						middleware.RequestID(
							middleware.LogRequest(app.Logger)(
								middleware.BeginRequestTx(app.DB, app.Logger)(mux),
							),
						),
					),
				),
			),
		),
	)
}
