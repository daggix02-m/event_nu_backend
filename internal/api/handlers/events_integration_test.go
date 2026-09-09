package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestCreateVenueBadJSON verifies a malformed CreateVenue body returns 400
// before any database call.
func TestCreateVenueBadJSON(t *testing.T) {
	_, h := newTestApp(t)
	_, tok, _ := registerAndToken(t, h, "badven")

	rec := doJSON(t, h, http.MethodPost, "/api/v1/venues", `{not json`, tok)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create venue bad JSON: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestUpdateEventDraftSuccess verifies the PATCH /api/v1/events/{id} happy
// path end-to-end: an organizer edits their own draft and the response echoes
// the updated title with the original id.
func TestUpdateEventDraftSuccess(t *testing.T) {
	_, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "updorg", fmt.Sprintf("Update Org %d", time.Now().UnixNano()))

	// Venue + draft event (no publish: only drafts are editable).
	rec := doJSON(t, h, http.MethodPost, "/api/v1/venues",
		fmt.Sprintf(`{"name":"Venue %d","latitude":40.71,"longitude":-74.00,"city":"NYC"}`, time.Now().UnixNano()), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create venue: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	venueID := idFromCreate(t, rec, "id")

	start := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events",
		fmt.Sprintf(`{"title":"Draft Event","starts_at":%q,"venue_id":%q,"price_is_free":true}`, start, venueID), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create event: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	eventID := idFromCreate(t, rec, "id")

	rec = doJSON(t, h, http.MethodPatch, "/api/v1/events/"+eventID,
		fmt.Sprintf(`{"title":"Draft Event v2","starts_at":%q,"venue_id":%q,"price_is_free":true}`, start, venueID), orgTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("update event: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Data struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	data := map[string]any{"id": got.Data.ID, "title": got.Data.Title}
	if data["id"] != eventID {
		t.Fatalf("updated event id = %v, want %s", data["id"], eventID)
	}
	if data["title"] != "Draft Event v2" {
		t.Fatalf("updated title = %v, want %q", data["title"], "Draft Event v2")
	}
}

// TestUpdateEventForbiddenForNonOwner verifies a user cannot PATCH an event
// owned by another organizer (403).
func TestUpdateEventForbiddenForNonOwner(t *testing.T) {
	_, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "updorg2", fmt.Sprintf("Update Org 2 %d", time.Now().UnixNano()))

	rec := doJSON(t, h, http.MethodPost, "/api/v1/venues",
		fmt.Sprintf(`{"name":"Venue %d","latitude":40.71,"longitude":-74.00,"city":"NYC"}`, time.Now().UnixNano()), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create venue: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	venueID := idFromCreate(t, rec, "id")
	start := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events",
		fmt.Sprintf(`{"title":"Their Event","starts_at":%q,"venue_id":%q,"price_is_free":true}`, start, venueID), orgTok)
	eventID := idFromCreate(t, rec, "id")

	_, intruder, _ := registerAndToken(t, h, "intruder")
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/events/"+eventID,
		fmt.Sprintf(`{"title":"Hijacked","starts_at":%q,"venue_id":%q,"price_is_free":true}`, start, venueID), intruder)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("update other owner's event: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}
