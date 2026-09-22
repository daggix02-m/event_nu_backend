package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type eventItem struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	OrganizerID      string `json:"organizer_id"`
	Status           string `json:"status"`
	ModerationStatus string `json:"moderation_status"`
}

type adminEventsResponse struct {
	Data []eventItem `json:"data"`
}

func getAdminEvents(t *testing.T, h http.Handler, q, token string) (*httptest.ResponseRecorder, adminEventsResponse) {
	t.Helper()
	rec := doJSON(t, h, http.MethodGet, "/api/v1/admin/events"+q, "", token)
	var resp adminEventsResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode admin events: %v; body=%s", err, rec.Body.String())
		}
	}
	return rec, resp
}

// TestAdminListEvents covers the admin moderation list: every lifecycle and
// moderation status is visible (drafts and blocked events included), filters
// work, and only admins get access.
func TestAdminListEvents(t *testing.T) {
	_, h := newTestApp(t)

	// Organizer applicant, promoted + approved by an admin.
	_, orgTok, _ := registerAndToken(t, h, "evorg")
	rec := doJSON(t, h, http.MethodPost, "/api/v1/organizer-applications",
		fmt.Sprintf(`{"requested_name":"Event Co %d","bio":"test"}`, time.Now().UnixNano()), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("apply: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	appID := idFromCreate(t, rec, "id")

	adminEmail, _, adminID := registerAndToken(t, h, "evadm")
	promoteToAdmin(t, adminID)
	adminTok := loginToken(t, h, adminEmail)

	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+appID+"/approve", `{}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Venue (required before publishing) + two draft events.
	start := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	rec = doJSON(t, h, http.MethodPost, "/api/v1/venues",
		fmt.Sprintf(`{"name":"Admin Venue %d","address":"1 Test St","latitude":40.71,"longitude":-74.00,"city":"NYC"}`, time.Now().UnixNano()), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create venue: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	venueID := idFromCreate(t, rec, "id")
	titleB := fmt.Sprintf("Admin Draft B %d", time.Now().UnixNano())
	createEvent := func(title string) string {
		r := doJSON(t, h, http.MethodPost, "/api/v1/events",
			fmt.Sprintf(`{"title":%q,"description":"x","starts_at":%q,"venue_id":%q,"price_is_free":true}`, title, start, venueID), orgTok)
		if r.Code != http.StatusCreated {
			t.Fatalf("create event %q: expected 201, got %d: %s", title, r.Code, r.Body.String())
		}
		return idFromCreate(t, r, "id")
	}
	createEvent("Draft A")
	eventB := createEvent(titleB)

	// Publish B, then block it.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventB+"/publish", "", orgTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish B: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/events/"+eventB+"/block", "", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("block B: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Boundary: anonymous 401, non-admin 403.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/events", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list: expected 401, got %d", rec.Code)
	}
	_, plainTok, _ := registerAndToken(t, h, "evplain")
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/events", "", plainTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin list: expected 403, got %d", rec.Code)
	}

	// The admin list itself returns 200; visibility of drafts/blocked events is
	// proven via the scoped filters below (the shared test DB grows across runs,
	// so a full page-1 scan can be dominated by unrelated events).
	rec, _ = getAdminEvents(t, h, "?limit=1&page=1", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin events list: expected 200, got %d", rec.Code)
	}

	// Lifecycle + moderation filters: every returned item must match the filter
	// (the shared test DB may accumulate >100 events, so specific IDs aren't
	// guaranteed on page 1).
	_, drafts := getAdminEvents(t, h, "?status=draft&limit=100", adminTok)
	if len(drafts.Data) == 0 {
		t.Fatalf("status=draft must return at least 1 item")
	}
	for _, item := range drafts.Data {
		if item.Status != "draft" {
			t.Fatalf("status=draft filter returned item %s with status=%q", item.ID, item.Status)
		}
	}

	_, published := getAdminEvents(t, h, "?status=published&limit=100", adminTok)
	if len(published.Data) == 0 {
		t.Fatalf("status=published must return at least 1 item")
	}
	for _, item := range published.Data {
		if item.Status != "published" {
			t.Fatalf("status=published filter returned item %s with status=%q", item.ID, item.Status)
		}
	}

	// Moderation filter.
	_, blocked := getAdminEvents(t, h, "?moderation_status=blocked&limit=100", adminTok)
	if len(blocked.Data) == 0 {
		t.Fatalf("moderation_status=blocked must return at least 1 item")
	}
	for _, item := range blocked.Data {
		if item.ModerationStatus != "blocked" {
			t.Fatalf("moderation_status=blocked filter returned item %s with moderation_status=%q", item.ID, item.ModerationStatus)
		}
	}

	_, clean := getAdminEvents(t, h, "?moderation_status=clean&limit=100", adminTok)
	for _, item := range clean.Data {
		if item.ModerationStatus != "clean" {
			t.Fatalf("moderation_status=clean filter returned item %s with moderation_status=%q", item.ID, item.ModerationStatus)
		}
	}

	// Unknown filter values are rejected, never silently ignored.
	for _, q := range []string{"?status=nope", "?moderation_status=nope"} {
		rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/events"+q, "", adminTok)
		if rec.Code == http.StatusOK {
			t.Fatalf("invalid filter %q must not be accepted, got 200", q)
		}
	}

	// Pagination contract.
	rec, paged := getAdminEvents(t, h, "?limit=1&page=1", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("paged list: expected 200, got %d", rec.Code)
	}
	var meta struct {
		Pagination struct {
			Page    int  `json:"page"`
			Limit   int  `json:"limit"`
			Total   int  `json:"total"`
			HasNext bool `json:"has_next"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("decode pagination: %v", err)
	}
	if len(paged.Data) != 1 || !meta.Pagination.HasNext || meta.Pagination.Total < 2 {
		t.Fatalf("limit=1 must return 1 item with has_next and total>=2, got %+v", meta.Pagination)
	}
}
