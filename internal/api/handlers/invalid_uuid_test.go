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
	event := handlers.NewEventHandlers(app, nil, nil, nil)
	org := handlers.NewOrganizerHandlers(app, nil)
	comments := handlers.NewCommentHandlers(app, nil)
	saves := handlers.NewSaveHandlers(app, nil)
	shares := handlers.NewShareHandlers(app, nil)
	follows := handlers.NewFollowHandlers(app, nil)
	rsvp := handlers.NewRsvpHandlers(app, nil)
	reviews := handlers.NewReviewHandlers(app, nil)
	reports := handlers.NewReportHandlers(app, nil)
	notes := handlers.NewNotificationHandlers(app, nil)
	reminders := handlers.NewReminderHandlers(app, nil)
	venues := handlers.NewVenueHandlers(app, nil)
	adminH := handlers.NewAdminHandlers(app, nil)

	cases := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		path    string
	}{
		{"event detail", event.GetEvent, http.MethodGet, "/api/v1/events/{id}"},
		{"update event", event.UpdateEvent, http.MethodPatch, "/api/v1/events/{id}"},
		{"publish event", event.Publish, http.MethodPost, "/api/v1/events/{id}/publish"},
		{"list comments", comments.List, http.MethodGet, "/api/v1/events/{id}/comments"},
		{"create comment", comments.Create, http.MethodPost, "/api/v1/events/{id}/comments"},
		{"update comment", comments.Update, http.MethodPatch, "/api/v1/comments/{id}"},
		{"delete comment", comments.Delete, http.MethodDelete, "/api/v1/comments/{id}"},
		{"add like", event.AddLike, http.MethodPost, "/api/v1/events/{id}/like"},
		{"remove like", event.RemoveLike, http.MethodDelete, "/api/v1/events/{id}/like"},
		{"save event", saves.SaveEvent, http.MethodPost, "/api/v1/events/{id}/save"},
		{"unsave event", saves.UnsaveEvent, http.MethodDelete, "/api/v1/events/{id}/save"},
		{"record share", shares.RecordShare, http.MethodPost, "/api/v1/events/{id}/share"},
		{"organizer profile", follows.GetOrganizer, http.MethodGet, "/api/v1/organizers/{id}"},
		{"follow organizer", follows.Follow, http.MethodPost, "/api/v1/organizers/{id}/follow"},
		{"unfollow organizer", follows.Unfollow, http.MethodDelete, "/api/v1/organizers/{id}/follow"},
		{"approve application", org.Approve, http.MethodPost, "/api/v1/admin/organizer-applications/{id}/approve"},
		{"reject application", org.Reject, http.MethodPost, "/api/v1/admin/organizer-applications/{id}/reject"},
		{"rsvp event", rsvp.Create, http.MethodPost, "/api/v1/events/{id}/rsvp"},
		{"cancel rsvp", rsvp.Cancel, http.MethodDelete, "/api/v1/events/{id}/rsvp"},
		{"rsvp state", rsvp.State, http.MethodGet, "/api/v1/events/{id}/rsvp"},
		{"list reviews", reviews.List, http.MethodGet, "/api/v1/events/{id}/reviews"},
		{"create review", reviews.Create, http.MethodPost, "/api/v1/events/{id}/reviews"},
		{"update review", reviews.Update, http.MethodPatch, "/api/v1/reviews/{id}"},
		{"delete review", reviews.Delete, http.MethodDelete, "/api/v1/reviews/{id}"},
		{"report event", reports.ReportTarget("event"), http.MethodPost, "/api/v1/events/{id}/report"},
		{"report user", reports.ReportTarget("user"), http.MethodPost, "/api/v1/users/{id}/report"},
		{"report venue", reports.ReportTarget("venue"), http.MethodPost, "/api/v1/venues/{id}/report"},
		{"report comment", reports.ReportTarget("comment"), http.MethodPost, "/api/v1/comments/{id}/report"},
		{"venue detail", venues.Get, http.MethodGet, "/api/v1/venues/{id}"},
		{"update venue", venues.Update, http.MethodPatch, "/api/v1/venues/{id}"},
		{"mark notification read", notes.Read, http.MethodPost, "/api/v1/notifications/{id}/read"},
		{"create reminder", reminders.Create, http.MethodPost, "/api/v1/events/{id}/reminders"},
		{"delete reminder", reminders.Delete, http.MethodDelete, "/api/v1/events/{id}/reminders"},
		{"resolve report", adminH.ResolveReport, http.MethodPost, "/api/v1/admin/reports/{id}/resolve"},
		{"block event", adminH.BlockEvent, http.MethodPost, "/api/v1/admin/events/{id}/block"},
		{"restore event", adminH.RestoreEvent, http.MethodPost, "/api/v1/admin/events/{id}/restore"},
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
