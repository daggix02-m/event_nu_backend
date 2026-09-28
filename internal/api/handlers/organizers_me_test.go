package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
)

func TestMyOrganizerAndMyEvents(t *testing.T) {
	_, h := newTestApp(t)

	// 1. Non-organizer caller GET /me/organizer -> 404
	_, plainTok, _ := registerAndToken(t, h, "plain_user")
	rec := doJSON(t, h, http.MethodGet, "/api/v1/me/organizer", "", plainTok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-organizer, got %d: %s", rec.Code, rec.Body.String())
	}

	// Unauthenticated GET /me/organizer -> 401
	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/organizer", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauth, got %d", rec.Code)
	}

	// Non-organizer caller GET /me/events -> 200 with empty list
	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/events", "", plainTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for non-organizer my-events, got %d: %s", rec.Code, rec.Body.String())
	}
	var emptyEventsResp struct {
		Data       []dto.EventDTO     `json:"data"`
		Pagination dto.PaginationMeta `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &emptyEventsResp); err != nil {
		t.Fatalf("decode empty events: %v", err)
	}
	if len(emptyEventsResp.Data) != 0 || emptyEventsResp.Pagination.Total != 0 {
		t.Fatalf("expected 0 events for non-organizer, got %d", len(emptyEventsResp.Data))
	}

	// 2. Applicant applies and admin approves
	_, applicantTok, _ := registerAndToken(t, h, "applicant")
	orgName := fmt.Sprintf("My Org %d", time.Now().UnixNano())
	rec = doJSON(t, h, http.MethodPost, "/api/v1/organizer-applications",
		fmt.Sprintf(`{"requested_name":%q,"bio":"Organizing music festivals"}`, orgName), applicantTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("apply: %d: %s", rec.Code, rec.Body.String())
	}
	appID := idFromCreate(t, rec, "id")

	// Admin approves
	adminEmail, _, adminID := registerAndToken(t, h, "admin_user")
	promoteToAdmin(t, adminID)
	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"email":%q,"password":"password123"}`, adminEmail), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin login: %d", rec.Code)
	}
	var adminResp tokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &adminResp); err != nil {
		t.Fatalf("decode admin login: %v", err)
	}
	adminTok := adminResp.Data.AccessToken

	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+appID+"/approve", `{"notes":"Approved"}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin approve: %d: %s", rec.Code, rec.Body.String())
	}

	// 3. After admin approves, applicant can GET /me/organizer and gets their own profile
	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/organizer", "", applicantTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("applicant GET /me/organizer after approval: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var myOrgResp struct {
		Data dto.MyOrganizerDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &myOrgResp); err != nil {
		t.Fatalf("decode my organizer: %v", err)
	}
	if myOrgResp.Data.Name != orgName {
		t.Fatalf("expected name %q, got %q", orgName, myOrgResp.Data.Name)
	}
	if myOrgResp.Data.EventCount != 0 {
		t.Fatalf("expected event_count 0, got %d", myOrgResp.Data.EventCount)
	}
	// logo_url degrades to "" rather than 500
	if myOrgResp.Data.LogoURL != "" {
		t.Fatalf("expected empty logo_url for organizer without logo, got %q", myOrgResp.Data.LogoURL)
	}

	// 4. Create venue and create draft event
	rec = doJSON(t, h, http.MethodPost, "/api/v1/venues",
		`{"name":"Main Hall","address":"123 Main St","latitude":40.71,"longitude":-74.00,"city":"NYC"}`, applicantTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create venue: %d: %s", rec.Code, rec.Body.String())
	}
	venueID := idFromCreate(t, rec, "id")

	start := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	eventTitle := fmt.Sprintf("Draft Event %d", time.Now().UnixNano())
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events",
		fmt.Sprintf(`{"title":%q,"description":"A secret draft","starts_at":%q,"venue_id":%q,"price_is_free":true}`,
			eventTitle, start, venueID), applicantTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create draft event: %d: %s", rec.Code, rec.Body.String())
	}
	draftEventID := idFromCreate(t, rec, "id")

	// 5. Draft is invisible via public GET /events
	rec = doJSON(t, h, http.MethodGet, "/api/v1/events?q="+url.QueryEscape(eventTitle), "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list public events: %d", rec.Code)
	}
	if containsEventID(t, rec, draftEventID) {
		t.Fatalf("draft event %s must be invisible via public GET /events", draftEventID)
	}

	// 6. Draft is visible to owner on GET /me/events
	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/events", "", applicantTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /me/events: %d: %s", rec.Code, rec.Body.String())
	}
	var myEventsResp struct {
		Data       []dto.EventDTO     `json:"data"`
		Pagination dto.PaginationMeta `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &myEventsResp); err != nil {
		t.Fatalf("decode my events: %v", err)
	}
	foundDraft := false
	for _, e := range myEventsResp.Data {
		if e.ID == draftEventID {
			foundDraft = true
			if e.Status != "draft" {
				t.Fatalf("expected status draft, got %s", e.Status)
			}
		}
	}
	if !foundDraft {
		t.Fatalf("draft event %s not found in owner's /me/events", draftEventID)
	}

	// 7. Cross-user isolation: another organizer cannot see applicant's draft in their /me/events
	_, otherOrgTok, _ := registerAndToken(t, h, "other_org")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/organizer-applications",
		fmt.Sprintf(`{"requested_name":"Other Org %d"}`, time.Now().UnixNano()), otherOrgTok)
	otherAppID := idFromCreate(t, rec, "id")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+otherAppID+"/approve", `{"notes":"Approved"}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve second organizer: %d", rec.Code)
	}

	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/events", "", otherOrgTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("other org GET /me/events: %d", rec.Code)
	}
	var otherEventsResp struct {
		Data []dto.EventDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &otherEventsResp); err != nil {
		t.Fatalf("decode other events: %v", err)
	}
	for _, e := range otherEventsResp.Data {
		if e.ID == draftEventID {
			t.Fatalf("cross-user isolation violated: other organizer saw draft event %s", draftEventID)
		}
	}

	// 8. Status filter on GET /me/events
	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/events?status=draft", "", applicantTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /me/events?status=draft: %d", rec.Code)
	}
	var draftEventsResp struct {
		Data []dto.EventDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &draftEventsResp); err != nil {
		t.Fatalf("decode draft events: %v", err)
	}
	if len(draftEventsResp.Data) == 0 {
		t.Fatalf("expected at least 1 draft event, got 0")
	}

	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/events?status=published", "", applicantTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /me/events?status=published: %d", rec.Code)
	}
	var publishedEventsResp struct {
		Data []dto.EventDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &publishedEventsResp); err != nil {
		t.Fatalf("decode published events: %v", err)
	}
	for _, e := range publishedEventsResp.Data {
		if e.ID == draftEventID {
			t.Fatalf("draft event %s should not appear in status=published filter", draftEventID)
		}
	}

	// 9. Pagination bounds and validation on /me/events
	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/events?page=0", "", applicantTok)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for page=0, got %d", rec.Code)
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/events?limit=0", "", applicantTok)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for limit=0, got %d", rec.Code)
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/events?limit=101", "", applicantTok)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for limit=101, got %d", rec.Code)
	}

	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/events?page=1&limit=1", "", applicantTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for page=1&limit=1, got %d", rec.Code)
	}
	var pageResp struct {
		Data       []dto.EventDTO     `json:"data"`
		Pagination dto.PaginationMeta `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pageResp); err != nil {
		t.Fatalf("decode page resp: %v", err)
	}
	if pageResp.Pagination.Page != 1 || pageResp.Pagination.Limit != 1 {
		t.Fatalf("pagination meta mismatch: %+v", pageResp.Pagination)
	}

	// 10. Check event count in GET /me/organizer now reflects the created event
	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/organizer", "", applicantTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("applicant GET /me/organizer after event create: %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &myOrgResp); err != nil {
		t.Fatalf("decode my organizer: %v", err)
	}
	if myOrgResp.Data.EventCount < 1 {
		t.Fatalf("expected event_count >= 1 after event create, got %d", myOrgResp.Data.EventCount)
	}
}
