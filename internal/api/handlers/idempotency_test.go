package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
)

// doJSONKey is doJSON plus an optional Idempotency-Key header.
func doJSONKey(t *testing.T, h http.Handler, method, path, body, token, idemKey string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// approveOrganizerAndVenue walks the full organizer approval flow for an
// already-registered user and returns a venue they own.
func approveOrganizerAndVenue(t *testing.T, h http.Handler, orgTok, userID string) string {
	t.Helper()
	orgName := fmt.Sprintf("Idem Org %d", time.Now().UnixNano()%1e6)
	rec := doJSON(t, h, http.MethodPost, "/api/v1/organizer-applications",
		fmt.Sprintf(`{"requested_name":%q,"bio":"idempotency fixture"}`, orgName), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("apply: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	appID := idFromCreate(t, rec, "id")

	adminEmail, _, adminID := registerAndToken(t, h, "idadm")
	promoteToAdmin(t, adminID)
	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"email":%q,"password":"password123"}`, adminEmail), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin login: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var adminResp tokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &adminResp); err != nil {
		t.Fatalf("decode admin login: %v", err)
	}
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+appID+"/approve",
		`{}`, adminResp.Data.AccessToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin approve: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodPost, "/api/v1/venues",
		`{"name":"Idem Venue","latitude":40.71,"longitude":-74.0}`, orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create venue: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	return idFromCreate(t, rec, "id")
}

// eventBody builds a valid event create payload for a venue.
func eventBody(venueID, title string) string {
	start := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	return fmt.Sprintf(`{"title":%q,"description":"idempotency test",`+
		`"starts_at":%q,"venue_id":%q,"price_is_free":true}`, title, start, venueID)
}

func organizerIDForUser(t *testing.T, userID string) string {
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
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM organizers WHERE owner_user_id = $1`, userID).Scan(&id); err != nil {
		t.Fatalf("organizer for user: %v", err)
	}
	return id
}

func countEvents(t *testing.T, organizerID string) int {
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
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE organizer_id = $1`, organizerID).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

func eventIDFrom(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode event response: %v; body=%s", err, rec.Body.String())
	}
	id, _ := resp.Data["id"].(string)
	if id == "" {
		t.Fatalf("missing event id in response: %s", rec.Body.String())
	}
	return id
}

// TestIdempotentEventCreate proves the client-retryable write semantics for
// POST /events (spec §22): same key + same request replays the stored response
// without a duplicate row; same key + different request is a 409; no-key
// requests compose normally.
func TestIdempotentEventCreate(t *testing.T) {
	_, h := newTestApp(t)
	_, orgTok, userID := registerAndToken(t, h, "idemx")
	venueID := approveOrganizerAndVenue(t, h, orgTok, userID)
	orgID := organizerIDForUser(t, userID)

	key := fmt.Sprintf("evt-%d", time.Now().UnixNano())
	body := eventBody(venueID, "Idempotent Rave")

	// First request with a key -> 201, one row.
	rec := doJSONKey(t, h, http.MethodPost, "/api/v1/events", body, orgTok, key)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first request: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	firstID := eventIDFrom(t, rec)
	if n := countEvents(t, orgID); n != 1 {
		t.Fatalf("after first request: expected 1 event, got %d", n)
	}

	// Retry same key + identical body -> stored response replayed, no duplicate.
	rec = doJSONKey(t, h, http.MethodPost, "/api/v1/events", body, orgTok, key)
	if rec.Code != http.StatusCreated {
		t.Fatalf("replay: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if id := eventIDFrom(t, rec); id != firstID {
		t.Fatalf("replay must return the original event; got %s, want %s", id, firstID)
	}
	if n := countEvents(t, orgID); n != 1 {
		t.Fatalf("after replay: expected 1 event, got %d", n)
	}

	// Same key + different body -> 409, and the second event is NOT created.
	other := eventBody(venueID, "Different Rave")
	rec = doJSONKey(t, h, http.MethodPost, "/api/v1/events", other, orgTok, key)
	if rec.Code != http.StatusConflict {
		t.Fatalf("key reused: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "idempotency_key_reused") {
		t.Fatalf("expected idempotency_key_reused body, got %s", rec.Body.String())
	}
	if n := countEvents(t, orgID); n != 1 {
		t.Fatalf("after key reuse: expected 1 event, got %d", n)
	}

	// No-key request composes normally.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events",
		eventBody(venueID, "No Key Rave"), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("no-key request: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if n := countEvents(t, orgID); n != 2 {
		t.Fatalf("after no-key request: expected 2 events, got %d", n)
	}
}

// TestIdempotentEventCreateConcurrent races N requests with the same key and
// body. The unique (user_id, key) constraint must serialize them so exactly one
// event row is ever created — regardless of whether losers replay the winner's
// stored response (201) or report the conflict as in-progress.
func TestIdempotentEventCreateConcurrent(t *testing.T) {
	_, h := newTestApp(t)
	_, orgTok, userID := registerAndToken(t, h, "idemc")
	venueID := approveOrganizerAndVenue(t, h, orgTok, userID)
	orgID := organizerIDForUser(t, userID)

	key := fmt.Sprintf("evt-race-%d", time.Now().UnixNano())
	body := eventBody(venueID, "Concurrent Rave")

	const n = 4
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		codes  = map[int]int{}
		bodies []string
	)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			rec := doJSONKey(t, h, http.MethodPost, "/api/v1/events", body, orgTok, key)
			mu.Lock()
			defer mu.Unlock()
			codes[rec.Code]++
			bodies = append(bodies, rec.Body.String())
		}()
	}
	wg.Wait()

	for code, count := range codes {
		if code == http.StatusInternalServerError {
			t.Fatalf("no request may 500 during the race: %s", bodies)
		}
		if code != http.StatusCreated && code != http.StatusConflict {
			t.Fatalf("unexpected status %d (%dx): %s", code, count, bodies)
		}
	}
	if got := countEvents(t, orgID); got != 1 {
		t.Fatalf("concurrent same-key creates must yield exactly 1 event, got %d (%v)", got, codes)
	}
}
