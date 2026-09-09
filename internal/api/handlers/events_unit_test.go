package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// errBadTime produces the fieldError used for malformed time fields in event
// bodies. Locking the message guards the client-facing contract.
func TestErrBadTimeErrorMessage(t *testing.T) {
	want := `body contains incorrect JSON type for field "starts_at"`
	if got := errBadTime("starts_at").Error(); got != want {
		t.Fatalf("errBadTime message = %q, want %q", got, want)
	}
	wantEnd := `body contains incorrect JSON type for field "ends_at"`
	if got := errBadTime("ends_at").Error(); got != wantEnd {
		t.Fatalf("ends_at variant = %q, want %q", got, wantEnd)
	}
}

func parseRequestWithBody(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestParseEventRequestValid verifies full body decoding, including a set
// ends_at (the conditional branch that maps it into the domain event).
func TestParseEventRequestValid(t *testing.T) {
	body := `{"title":"Team Lunch","starts_at":"2027-03-01T12:00:00Z",` +
		`"ends_at":"2027-03-01T14:00:00Z","venue_id":"v1","price_is_free":true,"max_attendees":20}`
	e, err := parseEventRequest(httptest.NewRecorder(), parseRequestWithBody(t, body))
	if err != nil {
		t.Fatalf("parseEventRequest: %v", err)
	}
	if e.Title != "Team Lunch" {
		t.Fatalf("title = %q, want %q", e.Title, "Team Lunch")
	}
	wantStart := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	if !e.StartsAt.Equal(wantStart) {
		t.Fatalf("starts_at = %s, want %s", e.StartsAt, wantStart)
	}
	wantEnd := time.Date(2027, 3, 1, 14, 0, 0, 0, time.UTC)
	if e.EndsAt == nil || !e.EndsAt.Equal(wantEnd) {
		t.Fatalf("ends_at = %v, want %s", e.EndsAt, wantEnd)
	}
	if e.VenueID == nil || *e.VenueID != "v1" || !e.PriceIsFree {
		t.Fatalf("venue/price mapping wrong: venue=%v free=%v", e.VenueID, e.PriceIsFree)
	}
	if e.MaxAttendees == nil || *e.MaxAttendees != 20 {
		t.Fatalf("max_attendees = %v", e.MaxAttendees)
	}
}

// TestParseEventRequestNoEndsAt verifies ends_at stays nil when absent.
func TestParseEventRequestNoEndsAt(t *testing.T) {
	body := `{"title":"T","starts_at":"2027-03-01T12:00:00Z","venue_id":"v1"}`
	e, err := parseEventRequest(httptest.NewRecorder(), parseRequestWithBody(t, body))
	if err != nil {
		t.Fatalf("parseEventRequest: %v", err)
	}
	if e.EndsAt != nil {
		t.Fatalf("ends_at = %v, want nil", *e.EndsAt)
	}
}

// TestParseEventRequestInvalidStartsAt verifies a non-RFC3339 starts_at
// yields the starts_at field error.
func TestParseEventRequestInvalidStartsAt(t *testing.T) {
	body := `{"title":"T","starts_at":"tomorrow","venue_id":"v1"}`
	_, err := parseEventRequest(httptest.NewRecorder(), parseRequestWithBody(t, body))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	fe, ok := err.(*fieldError)
	if !ok || fe.field != "starts_at" {
		t.Fatalf("err = %#v, want fieldError{starts_at}", err)
	}
}

// TestParseEventRequestInvalidEndsAt verifies a non-RFC3339 ends_at inside
// the conditional block yields the ends_at field error.
func TestParseEventRequestInvalidEndsAt(t *testing.T) {
	body := `{"title":"T","starts_at":"2027-03-01T12:00:00Z","ends_at":"later-ish","venue_id":"v1"}`
	_, err := parseEventRequest(httptest.NewRecorder(), parseRequestWithBody(t, body))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	fe, ok := err.(*fieldError)
	if !ok || fe.field != "ends_at" {
		t.Fatalf("err = %#v, want fieldError{ends_at}", err)
	}
}

// TestParseEventRequestBadJSON verifies a malformed body surfaces as a
// decode error (and never reaches time parsing).
func TestParseEventRequestBadJSON(t *testing.T) {
	_, err := parseEventRequest(httptest.NewRecorder(), parseRequestWithBody(t, `{oops`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if _, ok := err.(*fieldError); ok {
		t.Fatalf("bad JSON must not map to a field error, got %#v", err)
	}
}
