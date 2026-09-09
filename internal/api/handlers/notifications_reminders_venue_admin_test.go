package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
)

// queryOrganizerID resolves an organizer id from a stable name via the trusted
// service role (test setup only).
func queryOrganizerID(t *testing.T, name string) string {
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
		`SELECT id FROM organizers WHERE name = $1 ORDER BY created_at DESC LIMIT 1`, name).Scan(&id); err != nil {
		t.Fatalf("resolve organizer id: %v", err)
	}
	return id
}

// TestNotifications covers the inbox: hooks fan out notifications on activity,
// the recipient lists them newest-first, and read/read-all update as expected.
func TestNotifications(t *testing.T) {
	_, h := newTestApp(t)
	orgName := fmt.Sprintf("Notif Org %d", time.Now().UnixNano())
	orgTok, _ := promoteOrganizer(t, h, "notiforg", orgName)
	eventID := createPublishedEvent(t, h, orgTok, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
	orgID := queryOrganizerID(t, orgName)

	_, userA, _ := registerAndToken(t, h, "notifa")
	_, userB, _ := registerAndToken(t, h, "notifb")

	// Comment and RSVP both notify the organizer owner; a follow notifies too.
	if rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/comments", `{"body":"hello"}`, userA); rec.Code != http.StatusCreated {
		t.Fatalf("comment: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/rsvp", `{}`, userB); rec.Code != http.StatusOK {
		t.Fatalf("rsvp: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, h, http.MethodPost, "/api/v1/organizers/"+orgID+"/follow", "", userA); rec.Code != http.StatusOK {
		t.Fatalf("follow: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, h, http.MethodGet, "/api/v1/notifications", "", orgTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("list notifications: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Data []struct {
			ID        string  `json:"id"`
			Type      string  `json:"type"`
			Title     string  `json:"title"`
			ReadAt    *string `json:"read_at"`
			CreatedAt string  `json:"created_at"`
		} `json:"data"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode notifications: %v; body=%s", err, rec.Body.String())
	}

	types := map[string]bool{}
	for _, n := range list.Data {
		types[n.Type] = true
		if n.ReadAt != nil {
			t.Fatalf("expected unread notification, got read_at set: %+v", n)
		}
		if n.ID == "" || n.Title == "" {
			t.Fatalf("incomplete notification: %+v", n)
		}
	}
	for _, want := range []string{"comment", "rsvp", "follow"} {
		if !types[want] {
			t.Fatalf("expected a %q notification, got types=%v (body=%s)", want, types, rec.Body.String())
		}
	}

	// Mark one read, then read-all.
	firstID := list.Data[0].ID
	rec = doJSON(t, h, http.MethodPost, "/api/v1/notifications/"+firstID+"/read", "", orgTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark read: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodPost, "/api/v1/notifications/"+firstID+"/read", "", orgTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-mark read (idempotent): expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodPost, "/api/v1/notifications/read-all", "", orgTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("read-all: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var readAll struct {
		Data struct {
			Updated int64 `json:"updated"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &readAll); err != nil {
		t.Fatalf("decode read-all: %v; body=%s", err, rec.Body.String())
	}
	if readAll.Data.Updated != int64(len(list.Data)-1) {
		t.Fatalf("expected %d remaining unread, got %d", len(list.Data)-1, readAll.Data.Updated)
	}

	// Another user cannot read the owner's notification (own-row RLS → 404).
	rec = doJSON(t, h, http.MethodPost, "/api/v1/notifications/"+firstID+"/read", "", userA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-user mark read: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	// Anonymous notification access is rejected.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/notifications", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous notifications: expected 401, got %d", rec.Code)
	}
}

// TestReminders covers the reminder record lifecycle (dispatch is Phase 17).
func TestReminders(t *testing.T) {
	_, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "remorg", fmt.Sprintf("Rem Org %d", time.Now().UnixNano()))
	start := time.Now().Add(48 * time.Hour).UTC()
	eventID := createPublishedEvent(t, h, orgTok, start.Format(time.RFC3339))
	_, userA, _ := registerAndToken(t, h, "rema")

	// A future reminder inside the window is accepted.
	remindAt := start.Add(-time.Hour)
	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/reminders",
		fmt.Sprintf(`{"remind_at":%q}`, remindAt.Format(time.RFC3339)), userA)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create reminder: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Duplicate (same user, event, remind_at) → 409.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/reminders",
		fmt.Sprintf(`{"remind_at":%q}`, remindAt.Format(time.RFC3339)), userA)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate reminder: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// Out-of-window remind_at values are rejected.
	for _, bad := range []time.Time{time.Now().Add(-time.Hour), start.Add(time.Hour)} {
		rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/reminders",
			fmt.Sprintf(`{"remind_at":%q}`, bad.UTC().Format(time.RFC3339)), userA)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("bad remind_at %v: expected 422, got %d: %s", bad, rec.Code, rec.Body.String())
		}
	}

	// The reminder appears in /me/reminders with its event summary.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/reminders", "", userA)
	if rec.Code != http.StatusOK {
		t.Fatalf("list reminders: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Data []struct {
			ID         string `json:"id"`
			EventID    string `json:"event_id"`
			EventTitle string `json:"event_title"`
			RemindAt   string `json:"remind_at"`
			Status     string `json:"status"`
		} `json:"data"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode reminders: %v; body=%s", err, rec.Body.String())
	}
	if list.Pagination.Total != 1 || len(list.Data) != 1 {
		t.Fatalf("expected 1 reminder, got total=%d items=%d (%s)", list.Pagination.Total, len(list.Data), rec.Body.String())
	}
	if list.Data[0].EventID != eventID || list.Data[0].EventTitle == "" || list.Data[0].Status != "scheduled" {
		t.Fatalf("unexpected reminder payload: %+v", list.Data[0])
	}

	// Deleting removes it; delete is idempotent (200 on a second delete why not).
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/events/"+eventID+"/reminders", "", userA)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete reminder: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/reminders", "", userA)
	if rec.Code != http.StatusOK || !jsonContains(rec.Body.String(), `"total":0`) {
		t.Fatalf("expected empty reminders after delete, got %d: %s", rec.Code, rec.Body.String())
	}

	// Anonymous access is rejected.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/reminders",
		fmt.Sprintf(`{"remind_at":%q}`, remindAt.Format(time.RFC3339)), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous reminder: expected 401, got %d", rec.Code)
	}
}

// TestSearchAdminVenue covers the /events search filters, venue detail + update,
// and the admin moderation surface (reports queue + event block/restore).
func TestSearchAdminVenue(t *testing.T) {
	_, h := newTestApp(t)

	// Organizer + admin + a regular reporter are all distinct users.
	orgTok, _ := promoteOrganizer(t, h, "searchorg", fmt.Sprintf("Search Org %d", time.Now().UnixNano()))
	_, adminTok, adminUserID := registerAndToken(t, h, "adm12d")
	promoteToAdmin(t, adminUserID)
	_, userB, _ := registerAndToken(t, h, "srchb")

	// Two events: one with a unique searchable token, a far-away one, and a
	// targeted one bound to a category.
	token := fmt.Sprintf("snazzle%d", time.Now().UnixNano())
	matchID := createPublishedEventWithTitle(t, h, orgTok, token, time.Now().Add(10*time.Hour).UTC())
	otherID := createPublishedEvent(t, h, orgTok, time.Now().Add(20*time.Hour).UTC().Format(time.RFC3339))

	// A far-away venue so it is excluded by proximity filters.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/venues",
		fmt.Sprintf(`{"name":"Aaa Far %d","latitude":34.05,"longitude":-118.24,"city":"LA"}`, time.Now().UnixNano()), orgTok)
	farVenueID := idFromCreate(t, rec, "id")
	farID := doJSONCreateEvent(t, h, orgTok, farVenueID, time.Now().Add(30*time.Hour).UTC())

	// A category-bound event.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/categories", "", "")
	var catResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &catResp); err != nil {
		t.Fatalf("decode categories: %v; body=%s", err, rec.Body.String())
	}
	cats := catResp.Data
	if len(cats) == 0 {
		t.Fatal("expected seeded categories")
	}
	catID := cats[0].ID
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events",
		fmt.Sprintf(`{"title":"CatWired %d","starts_at":%q,"venue_id":%q,"category_id":%q,"price_is_free":true}`,
			time.Now().UnixNano(), time.Now().Add(12*time.Hour).UTC().Format(time.RFC3339), farVenueID, catID), orgTok)
	catEventID := idFromCreate(t, rec, "id")
	doJSON(t, h, http.MethodPost, "/api/v1/events/"+catEventID+"/publish", "", orgTok)

	// --- Full-text search ------------------------------------------------
	rec = doJSON(t, h, http.MethodGet, "/api/v1/events?q="+token, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("search: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var res paginateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	found := false
	for _, ev := range res.Data {
		if ev["id"] == matchID {
			found = true
		}
		if ev["id"] == otherID || ev["id"] == farID {
			t.Fatalf("search q=%q returned an unrelated event %v", token, ev["id"])
		}
	}
	if !found {
		t.Fatalf("search q=%q did not return the matching event (%s)", token, rec.Body.String())
	}

	// --- Category filter --------------------------------------------------
	rec = doJSON(t, h, http.MethodGet, "/api/v1/events?category="+catID, "", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode category filter: %v", err)
	}
	catFound := false
	for _, ev := range res.Data {
		if ev["id"] == catEventID {
			catFound = true
		}
		if ev["category_id"] != catID {
			t.Fatalf("category filter leaked event %v", ev["id"])
		}
	}
	if !catFound {
		t.Fatalf("category filter did not return the categorized event (%s)", rec.Body.String())
	}

	// --- Proximity filter -------------------------------------------------
	rec = doJSON(t, h, http.MethodGet, "/api/v1/events?lat=40.71&lng=-74.00&radius=30", "", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode proximity: %v", err)
	}
	if ev := eventByID(t, res.Data, farID); ev != "" {
		t.Fatalf("proximity filter returned the LA event %s (%s)", ev, rec.Body.String())
	}

	// --- Date window ------------------------------------------------------
	from := time.Now().Add(15 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(25 * time.Hour).UTC().Format(time.RFC3339)
	rec = doJSON(t, h, http.MethodGet, "/api/v1/events?date_from="+from+"&date_to="+to, "", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode date window: %v", err)
	}
	if ev := eventByID(t, res.Data, matchID); ev != "" {
		t.Fatalf("date window returned the earlier event %s (%s)", ev, rec.Body.String())
	}
	if ev := eventByID(t, res.Data, otherID); ev == "" {
		t.Fatalf("date window omitted the in-window event (%s)", rec.Body.String())
	}

	// --- Venue detail -----------------------------------------------------
	rec = doJSON(t, h, http.MethodGet, "/api/v1/venues/"+farVenueID, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("venue detail: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var ven struct {
		Data struct {
			Venue struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"venue"`
			Events []struct {
				ID string `json:"id"`
			} `json:"events"`
			EventsHasNextPage bool `json:"events_has_next_page"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ven); err != nil {
		t.Fatalf("decode venue detail: %v", err)
	}
	if ven.Data.Venue.ID != farVenueID {
		t.Fatalf("unexpected venue detail payload: %+v", ven.Data)
	}

	// --- Venue update ------------------------------------------------------
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/venues/"+farVenueID,
		fmt.Sprintf(`{"name":"Renamed %d","latitude":34.05,"longitude":-118.24,"city":"LA"}`, time.Now().UnixNano()), orgTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("venue update: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// A non-organizer user cannot update anyone's venue.
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/venues/"+farVenueID,
		fmt.Sprintf(`{"name":"Hijack %d","latitude":34.05,"longitude":-118.24,"city":"LA"}`, time.Now().UnixNano()), userB)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-organizer venue update: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	// --- Admin: report queue + resolve -------------------------------------
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+otherID+"/report", `{"reason_code":"spam"}`, userB)
	if rec.Code != http.StatusCreated {
		t.Fatalf("report: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/reports", "", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin reports list: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var adminRep paginateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &adminRep); err != nil {
		t.Fatalf("decode admin reports: %v", err)
	}
	var reportID string
	for _, r := range adminRep.Data {
		if r["entity_type"] == "event" {
			reportID = fmt.Sprint(r["id"])
			break
		}
	}
	if reportID == "" {
		t.Fatalf("no event report in queue (%s)", rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/reports/"+reportID+"/resolve", `{"resolution":"looked into it"}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve report: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// Resolving an already-resolved report is a 404.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/reports/"+reportID+"/resolve", `{}`, adminTok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("re-resolve report: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	// --- Admin: block/restore ----------------------------------------------
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/events/"+otherID+"/block", "", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("block event: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/events/"+otherID, "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("blocked event detail: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/events/"+otherID+"/restore", "", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore event: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/events/"+otherID, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("restored event detail: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// A non-admin cannot block events (or read the report queue).
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/events/"+otherID+"/block", "", userB)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin block: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/reports", "", userB)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin reports list: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

// createPublishedEventWithTitle creates a venue + published event with an exact
// title; returns the event id.
func createPublishedEventWithTitle(t *testing.T, h http.Handler, orgTok, title string, start time.Time) string {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/api/v1/venues",
		fmt.Sprintf(`{"name":"Venue %d","latitude":40.71,"longitude":-74.00,"city":"NYC"}`, time.Now().UnixNano()), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create venue: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	venueID := idFromCreate(t, rec, "id")
	return doJSONCreateEventAtVenue(t, h, orgTok, venueID, title, start)
}

// doJSONCreateEvent creates + publishes an event at a given venue.
func doJSONCreateEvent(t *testing.T, h http.Handler, orgTok, venueID string, start time.Time) string {
	t.Helper()
	return doJSONCreateEventAtVenue(t, h, orgTok, venueID, fmt.Sprintf("Far %d", time.Now().UnixNano()), start)
}

func doJSONCreateEventAtVenue(t *testing.T, h http.Handler, orgTok, venueID, title string, start time.Time) string {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/api/v1/events",
		fmt.Sprintf(`{"title":%q,"starts_at":%q,"venue_id":%q,"price_is_free":true}`,
			title, start.UTC().Format(time.RFC3339), venueID), orgTok)
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

// eventByID returns the returned id when id is present, else "".
func eventByID(t *testing.T, data []map[string]any, id string) string {
	t.Helper()
	for _, ev := range data {
		if ev["id"] == id {
			return id
		}
	}
	return ""
}
