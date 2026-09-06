package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/handlers"
	"github.com/daggix02-m/event_nu/backend/internal/api/metrics"
)

// TestMetricsHandlerExposesPrometheusText checks the /metrics handler without a
// database: the handler only renders the registry and never touches the tx
// context. A DB-backed counter check lives in routes tests.
func TestMetricsHandlerExposesPrometheusText(t *testing.T) {
	app := &api.Application{Metrics: metrics.NewRegistry()}
	app.Metrics.Hit("GET", 200)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	handlers.Metrics(app)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != metrics.ContentType {
		t.Fatalf("expected content type %q, got %q", metrics.ContentType, ct)
	}

	body := rec.Body.String()
	for _, want := range []string{
		`http_requests_total{method="GET",status="200"} 1`,
		"process_goroutines",
		"process_alloc_bytes",
		"process_gc_count",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q:\n%s", want, body)
		}
	}
}
