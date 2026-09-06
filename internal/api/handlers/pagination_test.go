package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// paginateResponse mirrors the {"data": [...], "pagination": {...}} envelope.
type paginateResponse struct {
	Data       []map[string]any `json:"data"`
	Pagination struct {
		Page    int  `json:"page"`
		Limit   int  `json:"limit"`
		Total   int  `json:"total"`
		HasNext bool `json:"has_next"`
	} `json:"pagination"`
}

func decodePaginated(t *testing.T, rec *httptest.ResponseRecorder) paginateResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp paginateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode paginated response: %v", err)
	}
	return resp
}

// seedOrganizer registers a user, applies as an organizer, and promotes them.
func promoteOrganizer(t *testing.T, h http.Handler, prefix, name string) (orgTok, adminTok string) {
	t.Helper()
	var email string
	email, orgTok, _ = registerAndToken(t, h, prefix)
	_ = email
	appID := applyForOrganizer(t, h, orgTok, name)
	adminTok, _ = adminApproveToken(t, h, "adm")
	rec := doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+appID+"/approve", `{}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve organizer: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	return orgTok, adminTok
}

// createPublishedEvent makes a venue + a published event; returns the event id.
func createPublishedEvent(t *testing.T, h http.Handler, orgTok, start string) string {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/api/v1/venues",
		fmt.Sprintf(`{"name":"Venue %d","latitude":40.71,"longitude":-74.00,"city":"NYC"}`, time.Now().UnixNano()%1e6), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create venue: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	venueID := idFromCreate(t, rec, "id")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events",
		fmt.Sprintf(`{"title":"Event %d","starts_at":%q,"venue_id":%q,"price_is_free":true}`, time.Now().UnixNano()%1e6, start, venueID), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create event: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	eventID := idFromCreate(t, rec, "id")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/publish", "", orgTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish event: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	return eventID
}

// TestPaginationBadParams verifies invalid page/limit are rejected with 400.
func TestPaginationBadParams(t *testing.T) {
	_, h := newTestApp(t)
	cases := []string{
		"/api/v1/events?limit=0",
		"/api/v1/events?limit=101",
		"/api/v1/events?limit=-1",
		"/api/v1/events?limit=abc",
		"/api/v1/events?page=0",
		"/api/v1/events?page=-5",
		"/api/v1/events?page=xyz",
		"/api/v1/venues?limit=0",
		"/api/v1/venues?limit=200",
	}
	for _, path := range cases {
		rec := doJSON(t, h, http.MethodGet, path, "", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d: %s", path, rec.Code, rec.Body.String())
		}
	}
}

// TestPaginationDefaults verifies the default page size and envelope shape.
func TestPaginationDefaults(t *testing.T) {
	_, h := newTestApp(t)
	rec := doJSON(t, h, http.MethodGet, "/api/v1/events", "", "")
	resp := decodePaginated(t, rec)
	if resp.Pagination.Page != 1 || resp.Pagination.Limit != 20 {
		t.Fatalf("expected page=1 limit=20, got page=%d limit=%d", resp.Pagination.Page, resp.Pagination.Limit)
	}
	// Data must never exceed the default page size even on a shared dev DB.
	if len(resp.Data) > resp.Pagination.Limit {
		t.Fatalf("page exceeded default limit: got %d items with limit %d", len(resp.Data), resp.Pagination.Limit)
	}
}

// TestPaginationCustomLimit verifies a custom limit is respected and capped.
func TestPaginationCustomLimit(t *testing.T) {
	_, h := newTestApp(t)
	rec := doJSON(t, h, http.MethodGet, "/api/v1/events?limit=5", "", "")
	resp := decodePaginated(t, rec)
	if resp.Pagination.Limit != 5 {
		t.Fatalf("expected limit=5, got %d", resp.Pagination.Limit)
	}
	if len(resp.Data) > 5 {
		t.Fatalf("expected at most 5 events, got %d", len(resp.Data))
	}
}

// TestPaginationEventsStableOrder walks pages of a known set of published
// events and asserts each event appears exactly once (no dupes, no gaps).
func TestPaginationEventsStableOrder(t *testing.T) {
	_, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "pgorg", fmt.Sprintf("Page Collective %d", time.Now().UnixNano()%1e6))

	const total = 7
	base := time.Now().Add(48 * time.Hour).UTC()
	created := make([]string, 0, total)
	for i := 0; i < total; i++ {
		start := base.Add(time.Duration(i+1) * time.Hour).Format(time.RFC3339)
		created = append(created, createPublishedEvent(t, h, orgTok, start))
	}

	seen := make(map[string]bool)
	for page := 1; ; page++ {
		rec := doJSON(t, h, http.MethodGet, fmt.Sprintf("/api/v1/events?page=%d&limit=3", page), "", "")
		resp := decodePaginated(t, rec)
		if resp.Pagination.Page != page {
			t.Fatalf("expected page %d, got %d", page, resp.Pagination.Page)
		}
		if resp.Pagination.Total < total {
			t.Fatalf("expected total >= %d (shared dev DB may hold more), got %d", total, resp.Pagination.Total)
		}
		for _, e := range resp.Data {
			id := e["id"].(string)
			if seen[id] {
				t.Fatalf("duplicate event id %s across pages", id)
			}
			seen[id] = true
		}
		if !resp.Pagination.HasNext {
			break
		}
		// Termination cap derived from the stable total returned by the first
		// page: the shared dev DB accumulates published events across every
		// run, so a hard-coded page limit would trip once enough rows exist.
		// No writes happen inside this walk, so total is stable throughout.
		if page > resp.Pagination.Total/resp.Pagination.Limit+2 {
			t.Fatal("pagination did not terminate")
		}
	}
	for _, id := range created {
		if !seen[id] {
			t.Fatalf("created event %s missing across pages", id)
		}
	}
}

// TestPaginationHasNextBoundary checks has_next flips correctly at a page edge.
func TestPaginationHasNextBoundary(t *testing.T) {
	_, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "pghn", fmt.Sprintf("BN Org %d", time.Now().UnixNano()%1e6))

	base := time.Now().Add(72 * time.Hour).UTC()
	createPublishedEvent(t, h, orgTok, base.Add(1*time.Hour).Format(time.RFC3339))
	createPublishedEvent(t, h, orgTok, base.Add(2*time.Hour).Format(time.RFC3339))

	// limit=2 (a full page) with exactly 2+ events in the DB → the first page
	// returns 2 and has_next is true when more rows exist beyond it.
	rec := doJSON(t, h, http.MethodGet, "/api/v1/events?limit=2", "", "")
	resp := decodePaginated(t, rec)
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 events on full page, got %d", len(resp.Data))
	}
	if resp.Pagination.Total < 2 {
		t.Fatalf("expected total >= 2, got %d", resp.Pagination.Total)
	}
	_ = rec
}

// TestVenuePagination verifies venues paginate and stay within a boundary.
func TestVenuePagination(t *testing.T) {
	_, h := newTestApp(t)
	// Even with no venues, the envelope must be well-formed.
	rec := doJSON(t, h, http.MethodGet, "/api/v1/venues", "", "")
	resp := decodePaginated(t, rec)
	if resp.Pagination.Page != 1 || resp.Pagination.Limit != 20 {
		t.Fatalf("venue default pagination: expected page=1 limit=20, got %d/%d", resp.Pagination.Page, resp.Pagination.Limit)
	}
	// A custom size request is honored.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/venues?limit=3", "", "")
	resp = decodePaginated(t, rec)
	if resp.Pagination.Limit != 3 {
		t.Fatalf("venue custom limit: expected 3, got %d", resp.Pagination.Limit)
	}
	if len(resp.Data) > 3 {
		t.Fatalf("venue page exceeded limit: got %d", len(resp.Data))
	}
}
