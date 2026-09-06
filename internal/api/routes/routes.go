package routes

import (
	"net/http"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/handlers"
	"github.com/daggix02-m/event_nu/backend/internal/api/middleware"
)

// New builds the route table and wraps it in the middleware chain.
func New(app *api.Application) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handlers.Healthz(app))
	mux.HandleFunc("GET /readyz", handlers.Readyz(app))

	auth := handlers.NewAuthHandlers(app, app.Auth)
	email := handlers.NewEmailHandlers(app)
	org := handlers.NewOrganizerHandlers(app, app.Org)
	events := handlers.NewEventHandlers(app, app.Event)

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
	mux.Handle("GET /api/v1/events", public(events.ListEvents))
	mux.Handle("GET /api/v1/events/{id}", public(events.GetEvent))

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
	mux.Handle("POST /api/v1/organizer-applications", protectedIdem(org.Apply))
	mux.Handle("GET /api/v1/organizer-applications/me", protected(org.GetMyApplication))
	mux.Handle("POST /api/v1/venues", protectedIdem(events.CreateVenue))
	mux.Handle("POST /api/v1/events", protectedIdem(events.CreateEvent))
	mux.Handle("PATCH /api/v1/events/{id}", protected(events.UpdateEvent))
	mux.Handle("POST /api/v1/events/{id}/publish", protected(events.Publish))

	// Admin business actions stay in Go (they trigger side effects + audits).
	admin := func(h http.HandlerFunc) http.Handler {
		return middleware.RequireAdmin(app.Auth)(middleware.WithRole("admin")(middleware.SetRLS(h)))
	}
	mux.Handle("POST /api/v1/admin/organizer-applications/{id}/approve", admin(org.Approve))
	mux.Handle("POST /api/v1/admin/organizer-applications/{id}/reject", admin(org.Reject))

	allowed := make(map[string]struct{}, len(app.Config.CORSAllowedOrigins))
	for _, o := range app.Config.CORSAllowedOrigins {
		allowed[o] = struct{}{}
	}

	return middleware.RecoverPanic(app.Logger)(
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
	)
}
