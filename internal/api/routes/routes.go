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
	mux.HandleFunc("POST /api/v1/auth/register", auth.Register)
	mux.HandleFunc("POST /api/v1/auth/login", auth.Login)
	mux.HandleFunc("POST /api/v1/auth/refresh", auth.Refresh)
	mux.HandleFunc("POST /api/v1/auth/logout", auth.Logout)

	// Protected route group.
	protected := http.NewServeMux()
	protected.HandleFunc("GET /api/v1/users/me", auth.Me)
	mux.Handle("GET /api/v1/users/me", middleware.RequireAuth(app.Auth)(protected))

	allowed := make(map[string]struct{}, len(app.Config.CORSAllowedOrigins))
	for _, o := range app.Config.CORSAllowedOrigins {
		allowed[o] = struct{}{}
	}

	return middleware.RecoverPanic(app.Logger)(
		middleware.RequestTimeout(15*time.Second)(
			middleware.CORS(allowed)(
				middleware.SecureHeaders(
					middleware.RequestID(
						middleware.LogRequest(app.Logger)(mux),
					),
				),
			),
		),
	)
}