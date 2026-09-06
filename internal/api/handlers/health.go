package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// Healthz is the liveness probe — the process is up.
func Healthz(app *api.Application) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// Readyz is the readiness probe — dependencies (database) are reachable.
func Readyz(app *api.Application) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		if err := app.DB.Ping(ctx); err != nil {
			shared.WriteErrorJSON(w, http.StatusServiceUnavailable, "unavailable", "database unreachable")
			return
		}
		shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}
