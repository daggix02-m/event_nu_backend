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
	service := func(h http.HandlerFunc) http.Handler {
		return middleware.WithRole("service")(middleware.SetRLS(h))
	}
	mux.Handle("POST /api/v1/auth/register", service(auth.Register))
	mux.Handle("POST /api/v1/auth/login", service(auth.Login))
	mux.Handle("POST /api/v1/auth/refresh", service(auth.Refresh))
	mux.Handle("POST /api/v1/auth/logout", service(auth.Logout))
	mux.Handle("POST /api/v1/auth/verify", service(email.VerifyPOST))
	mux.Handle("GET /api/v1/auth/verify", service(email.VerifyGET))

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
	mux.Handle("GET /api/v1/users/me", protected(auth.Me))
	mux.Handle("POST /api/v1/organizer-applications", protected(org.Apply))
	mux.Handle("GET /api/v1/organizer-applications/me", protected(org.GetMyApplication))
	mux.Handle("POST /api/v1/venues", protected(events.CreateVenue))
	mux.Handle("POST /api/v1/events", protected(events.CreateEvent))
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
