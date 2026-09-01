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

	// Protected routes: authenticate, then apply RLS as the authenticated user.
	protected := func(h http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(app.Auth)(middleware.SetRLS(h))
	}
	mux.Handle("GET /api/v1/users/me", protected(auth.Me))

	allowed := make(map[string]struct{}, len(app.Config.CORSAllowedOrigins))
	for _, o := range app.Config.CORSAllowedOrigins {
		allowed[o] = struct{}{}
	}

	return middleware.RecoverPanic(app.Logger)(
		middleware.RequestTimeout(15*time.Second)(
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