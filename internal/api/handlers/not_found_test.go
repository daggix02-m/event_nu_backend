package handlers_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestWellFormedMissingUUIDIs404 pins the sentinel mapping in
// (app)AppError: a well-formed but nonexistent resource id must produce a
// clean 404 (not_found), never an internal 500. Uses a UUID that is never
// seeded, so the row is guaranteed absent regardless of dev-DB state.
func TestWellFormedMissingUUIDIs404(t *testing.T) {
	_, h := newTestApp(t)
	const missing = "9f5f0a2c-0000-0000-0000-000000000001"

	rec := doJSON(t, h, http.MethodGet, "/api/v1/events/"+missing, "", "")
	assertNotFound(t, "event detail", rec)

	orgTok, _ := promoteOrganizer(t, h, "nfo", fmt.Sprintf("nf-%d", time.Now().UnixNano()))
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/events/"+missing,
		`{"title":"nope","starts_at":"2030-01-01T12:00:00Z","price_is_free":true}`, orgTok)
	assertNotFound(t, "update event", rec)

	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+missing+"/publish", "", orgTok)
	assertNotFound(t, "publish event", rec)
}

func assertNotFound(t *testing.T, name string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusNotFound {
		t.Fatalf("%s: expected 404, got %d: %s", name, rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"not_found"`) {
		t.Fatalf("%s: expected not_found code, got %s", name, rec.Body.String())
	}
}
