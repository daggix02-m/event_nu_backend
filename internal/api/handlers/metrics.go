package handlers

import (
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/metrics"
)

// Metrics exposes the process/request counters in Prometheus text format. It is
// deliberately free of the request transaction: scraping must never touch the
// database, and the route is registered without auth or RLS (phase-09 WS13).
func Metrics(app *api.Application) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", metrics.ContentType)
		app.Metrics.WritePrometheus(w)
	}
}
