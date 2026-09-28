package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

type adminVenueItem struct {
	ID          string    `json:"id"`
	OrganizerID string    `json:"organizer_id"`
	Name        string    `json:"name"`
	Address     string    `json:"address"`
	Latitude    float64   `json:"latitude"`
	Longitude   float64   `json:"longitude"`
	PlaceID     string    `json:"place_id"`
	City        string    `json:"city"`
	CountryCode string    `json:"country_code"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type adminVenueListResponse struct {
	Data []adminVenueItem `json:"data"`
}

type adminVenueDetailResponse struct {
	Data adminVenueItem `json:"data"`
}

// createAdminOrganizer creates a standalone organizer under the console.
func createAdminOrganizer(t *testing.T, h http.Handler, adminTok, name string) string {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/api/v1/admin/organizers",
		fmt.Sprintf(`{"name":%q,"status":"active"}`, name), adminTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create admin organizer: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var org struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &org); err != nil {
		t.Fatalf("decode created organizer: %v; body=%s", err, rec.Body.String())
	}
	return org.Data.ID
}

func getAdminVenues(t *testing.T, h http.Handler, q, token string) (*httptest.ResponseRecorder, adminVenueListResponse) {
	t.Helper()
	rec := doJSON(t, h, http.MethodGet, "/api/v1/admin/venues"+q, "", token)
	var resp adminVenueListResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode admin venues: %v; body=%s", err, rec.Body.String())
		}
	}
	return rec, resp
}

// TestAdminVenueCreateAndDirectory covers organizer-bound provisioning,
// validation, unresolved-organizer rejection, and the directory row shape.
func TestAdminVenueCreateAndDirectory(t *testing.T) {
	_, h := newTestApp(t)
	adminTok, _ := adminApproveToken(t, h, "venadm")

	unique := time.Now().UnixNano()

	// Boundary: anonymous 401, non-admin 403.
	rec := doJSON(t, h, http.MethodGet, "/api/v1/admin/venues", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list: expected 401, got %d", rec.Code)
	}
	_, plainTok, _ := registerAndToken(t, h, "venplain")
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/venues", "", plainTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin list: expected 403, got %d", rec.Code)
	}

	orgID := createAdminOrganizer(t, h, adminTok, fmt.Sprintf("Venue House %d", unique))
	venueName := fmt.Sprintf("Main Hall %d", unique)

	body := fmt.Sprintf(`{
		"organizer_id": %q,
		"name": %q,
		"address": "1 Test St",
		"latitude": 41.39,
		"longitude": 2.17,
		"city": "Barcelona",
		"country_code": "ES"
	}`, orgID, venueName)
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/venues", body, adminTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create venue: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created adminVenueDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created venue: %v", err)
	}
	if created.Data.OrganizerID != orgID ||
		created.Data.Name != venueName ||
		created.Data.Status != "active" ||
		created.Data.City != "Barcelona" {
		t.Fatalf("created venue shape wrong: %+v", created.Data)
	}

	// Non-admin cannot create.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/venues", body, plainTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin create: expected 403, got %d", rec.Code)
	}

	// Unresolved organizer → 422.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/venues",
		`{"organizer_id":"9f5f0a2c-0000-0000-0000-000000000001","name":"Ghost Venue","latitude":0,"longitude":0}`, adminTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unresolved organizer: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// Validation 422s.
	for _, bad := range []string{
		fmt.Sprintf(`{"organizer_id":%q,"name":"","latitude":0,"longitude":0}`, orgID),
		fmt.Sprintf(`{"organizer_id":%q,"name":"Bad Lat","latitude":95,"longitude":12}`, orgID),
	} {
		rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/venues", bad, adminTok)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid venue %s: expected 422, got %d", bad, rec.Code)
		}
	}

	// Directory: search finds the venue by name (URL-escaped).
	rec, found := getAdminVenues(t, h, "?q="+url.QueryEscape(venueName)+"&limit=100", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("directory search: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(found.Data) != 1 || found.Data[0].ID != created.Data.ID {
		t.Fatalf("q=%s must find exactly the created venue, got %d rows", venueName, len(found.Data))
	}
}

// TestAdminVenueListFilters covers status filtering plus the pagination shape.
func TestAdminVenueListFilters(t *testing.T) {
	_, h := newTestApp(t)
	adminTok, _ := adminApproveToken(t, h, "venadm2")

	// Invalid status filter rejected.
	rec := doJSON(t, h, http.MethodGet, "/api/v1/admin/venues?status=nope", "", adminTok)
	if rec.Code == http.StatusOK {
		t.Fatalf("invalid status filter must not be accepted, got 200")
	}

	unique := time.Now().UnixNano()
	orgID := createAdminOrganizer(t, h, adminTok, fmt.Sprintf("Filter House %d", unique))
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/venues",
		fmt.Sprintf(`{"organizer_id":%q,"name":"Filter Hall %d","latitude":1,"longitude":2}`, orgID, unique), adminTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create venue: expected 201, got %d", rec.Code)
	}

	// status=active lists the new venue; status=disabled does not.
	rec, active := getAdminVenues(t, h, "?status=active&limit=100", adminTok)
	if rec.Code != http.StatusOK || len(active.Data) == 0 {
		t.Fatalf("status=active must return rows, got %d", rec.Code)
	}
	for _, v := range active.Data {
		if v.Status != "active" {
			t.Fatalf("status filter leaked non-active row: %+v", v)
		}
	}
	rec, disabled := getAdminVenues(t, h, "?status=disabled&limit=100", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=disabled: expected 200, got %d", rec.Code)
	}
	for _, v := range disabled.Data {
		if v.Status != "disabled" {
			t.Fatalf("disabled filter leaked: %+v", v)
		}
	}

	// Pagination contract.
	rec, paged := getAdminVenues(t, h, "?limit=1&page=1", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("paged venues: expected 200, got %d", rec.Code)
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
	if len(paged.Data) != 1 || meta.Pagination.Total < 1 {
		t.Fatalf("limit=1 must return 1 row with total>=1, got %+v", meta.Pagination)
	}
}
