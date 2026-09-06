package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/handlers"
)

// TestInvalidUUIDPathParamsReturn404 verifies every handler that binds a UUID
// path parameter rejects a malformed value with a clean 404 before any
// database (or service) access. The handlers run against an Application with
// nil services: reaching a service would panic, so a 404 (rather than a panic
// or 500) proves the guard fires first. DB-free by construction.
func TestInvalidUUIDPathParamsReturn404(t *testing.T) {
	app := &api.Application{Logger: testLogger()}
	event := handlers.NewEventHandlers(app, nil)
	org := handlers.NewOrganizerHandlers(app, nil)

	cases := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		path    string
	}{
		{"event detail", event.GetEvent, http.MethodGet, "/api/v1/events/{id}"},
		{"update event", event.UpdateEvent, http.MethodPatch, "/api/v1/events/{id}"},
		{"publish event", event.Publish, http.MethodPost, "/api/v1/events/{id}/publish"},
		{"approve application", org.Approve, http.MethodPost, "/api/v1/admin/organizer-applications/{id}/approve"},
		{"reject application", org.Reject, http.MethodPost, "/api/v1/admin/organizer-applications/{id}/reject"},
	}

	for _, tc := range cases {
		for _, bad := range []string{"not-a-uuid", "", "81f63f0a-", "9f5f0a2c-0000-0000-0000-00000000000Z"} {
			req := httptest.NewRequest(tc.method, strings.ReplaceAll(tc.path, "{id}", bad), nil)
			req.SetPathValue("id", bad)
			rec := httptest.NewRecorder()
			tc.handler(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s with id %q: expected 404, got %d: %s", tc.name, bad, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"not_found"`) {
				t.Fatalf("%s with id %q: expected not_found code, got %s", tc.name, bad, rec.Body.String())
			}
		}
	}
}
