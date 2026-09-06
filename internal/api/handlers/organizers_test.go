package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
)

type tokenResponse struct {
	Data struct {
		AccessToken string `json:"access_token"`
	} `json:"data"`
}

func registerAndToken(t *testing.T, h http.Handler, prefix string) (email, token, userID string) {
	t.Helper()
	email = fmt.Sprintf("%s+%d@test.example", prefix, time.Now().UnixNano())
	uname := fmt.Sprintf("%s%d", prefix[:3], time.Now().UnixNano()%1e6)
	rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/register",
		fmt.Sprintf(`{"email":%q,"password":"password123","username":%q}`, email, uname), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("register: %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			User         struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode register: %v", err)
	}
	return email, resp.Data.AccessToken, resp.Data.User.ID
}

// promoteToAdmin promotes a user using the trusted service role (test setup).
func promoteToAdmin(t *testing.T, userID string) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Fatal("DATABASE_URL not set")
	}
	pool, err := database.NewPoolWithRole(context.Background(), dsn, "service")
	if err != nil {
		t.Fatalf("service pool: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role = 'admin' WHERE id = $1`, userID); err != nil {
		t.Fatalf("promote to admin: %v", err)
	}
}

func idFromCreate(t *testing.T, rec *httptest.ResponseRecorder, key string) string {
	t.Helper()
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	id, _ := resp.Data[key].(string)
	if id == "" {
		t.Fatalf("missing %s in response", key)
	}
	return id
}

func TestOrganizerVenueEventSlice(t *testing.T) {
	_, h := newTestApp(t)

	// --- Organizer application flow ---
	_, orgTok, _ := registerAndToken(t, h, "org")
	orgName := fmt.Sprintf("Nightlife Collective %d", time.Now().UnixNano()%1e6)

	rec := doJSON(t, h, http.MethodPost, "/api/v1/organizer-applications",
		fmt.Sprintf(`{"requested_name":%q,"bio":"We throw parties"}`, orgName), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("apply: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	appID := idFromCreate(t, rec, "id")

	// Duplicate pending application conflicts.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/organizer-applications",
		fmt.Sprintf(`{"requested_name":%q,"bio":"again"}`, orgName), orgTok)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate apply: expected 409, got %d", rec.Code)
	}

	// Me endpoint returns the application.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/organizer-applications/me", "", orgTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("my application: expected 200, got %d", rec.Code)
	}

	// --- Non-admin cannot approve ---
	_, nonAdminTok, _ := registerAndToken(t, h, "plain")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+appID+"/approve",
		`{"notes":""}`, nonAdminTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin approve: expected 403, got %d", rec.Code)
	}

	// Unauthenticated approve is 401.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+appID+"/approve", `{}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth approve: expected 401, got %d", rec.Code)
	}

	// --- Admin approves, organizer is created ---
	adminEmail, _, adminID := registerAndToken(t, h, "admin")
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
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+appID+"/approve",
		`{"notes":"approved"}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin approve: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// --- Non-organizer cannot create a venue/event ---
	rec = doJSON(t, h, http.MethodPost, "/api/v1/venues",
		`{"name":"HQ","latitude":40.71,"longitude":-74.0}`, nonAdminTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-organizer venue: expected 403, got %d", rec.Code)
	}

	// --- Approved organizer creates a venue ---
	rec = doJSON(t, h, http.MethodPost, "/api/v1/venues",
		`{"name":"Warehouse 7","address":"7 Dockside","latitude":40.71,"longitude":-74.00,"city":"NYC"}`, orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create venue: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	venueID := idFromCreate(t, rec, "id")

	// --- Organizer creates a draft event ---
	start := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"title":"Warehouse Rave","description":"Bring a friend",`+
		`"starts_at":%q,"venue_id":%q,"price_is_free":true,"max_attendees":50}`, start, venueID)
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events", body, orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create event: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	eventID := idFromCreate(t, rec, "id")

	// Draft is NOT visible publicly.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/events", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list events: %d", rec.Code)
	}
	if containsEventID(t, rec, eventID) {
		t.Fatal("draft event must not be publicly visible")
	}

	// --- Publish ---
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/publish", "", orgTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Published event is publicly visible now.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/events", "", "")
	if rec.Code != http.StatusOK || !containsEventID(t, rec, eventID) {
		t.Fatalf("published event must be visible: %d", rec.Code)
	}

	// Public event detail (no auth).
	rec = doJSON(t, h, http.MethodGet, "/api/v1/events/"+eventID, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("event detail: expected 200, got %d", rec.Code)
	}

	// --- Another organizer cannot edit this event ---
	_, otherOrgTok, _ := registerAndToken(t, h, "other")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/organizer-applications",
		fmt.Sprintf(`{"requested_name":"Second Crew %d"}`, time.Now().UnixNano()%1e6), otherOrgTok)
	otherAppID := idFromCreate(t, rec, "id")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+otherAppID+"/approve", `{}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve second organizer: %d", rec.Code)
	}
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/events/"+eventID,
		fmt.Sprintf(`{"title":"Hijacked","starts_at":%q}`, start), otherOrgTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-organizer edit: expected 403, got %d", rec.Code)
	}

	// --- Categories are public ---
	rec = doJSON(t, h, http.MethodGet, "/api/v1/categories", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("categories: %d", rec.Code)
	}

	// Venue public list works.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/venues", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("venues: %d", rec.Code)
	}
}

func containsEventID(t *testing.T, rec *httptest.ResponseRecorder, id string) bool {
	t.Helper()
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode event list: %v", err)
	}
	for _, e := range resp.Data {
		if e["id"] == id {
			return true
		}
	}
	return false
}
