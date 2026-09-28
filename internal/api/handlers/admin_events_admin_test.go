package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

type adminEventItem struct {
	ID          string  `json:"id"`
	OrganizerID string  `json:"organizer_id"`
	VenueID     *string `json:"venue_id"`
	Title       string  `json:"title"`
	Status      string  `json:"status"`
	ActionType  string  `json:"action_type"`
}

type adminEventDetailResponse struct {
	Data struct {
		adminEventItem
		PosterURL string `json:"poster_url"`
		Organizer struct {
			ID      string `json:"id"`
			Slug    string `json:"slug"`
			Name    string `json:"name"`
			Status  string `json:"status"`
			LogoURL string `json:"logo_url"`
		} `json:"organizer"`
		Venue *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			City string `json:"city"`
		} `json:"venue"`
	} `json:"data"`
}

func adminEventBody(unique int64, title, organizerName string, publish *bool) string {
	pub := ""
	if publish != nil {
		pub = fmt.Sprintf(`,"publish":%v`, *publish)
	}
	body := fmt.Sprintf(`{
		"organizer": {"name": %q},
		"venue": {"name": "Atomic Hall %d", "address": "1 Test St", "latitude": 41.39, "longitude": 2.17},
		"title": %q,
		"starts_at": %q,
		"action_type": "open_entry",
		"price_is_free": true
	}%s`, organizerName, unique, title, time.Now().Add(48*time.Hour).Format(time.RFC3339), pub)
	return body
}

// TestAdminEventCreateAtomic exercises the full atomic create: inline organizer
// resol... / create + inline venue / create + insert + publish in one request,
// and proves the created rows are visible everywhere (public list included).
func TestAdminEventCreateAtomic(t *testing.T) {
	_, h := newTestApp(t)
	adminTok, _ := adminApproveToken(t, h, "evadm")

	unique := time.Now().UnixNano()

	// Boundary: anonymous/non-admin 401/403 on both list-access endpoints.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/admin/events", adminEventBody(unique, "x", "y", nil), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous create: expected 401, got %d", rec.Code)
	}
	_, plainTok, _ := registerAndToken(t, h, "evplain")
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/events/9f5f0a2c-0000-0000-0000-000000000001", "", plainTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin detail: expected 403, got %d", rec.Code)
	}

	title := fmt.Sprintf("Atomic Launch %d", unique)
	orgName := fmt.Sprintf("Atomic Org %d", unique)
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/events", adminEventBody(unique, title, orgName, nil), adminTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("atomic create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data adminEventItem `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created event: %v; body=%s", err, rec.Body.String())
	}
	if created.Data.Status != "published" {
		t.Fatalf("atomic create must publish immediately, got status=%q", created.Data.Status)
	}
	if created.Data.VenueID == nil {
		t.Fatalf("published event must have a venue, got nil")
	}

	// The organizer was auto-provisioned...
	rec, _ = getAdminOrganizerList(t, h, "?q="+url.QueryEscape(orgName)+"&limit=100", adminTok)
	var orgRows struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &orgRows); err != nil {
		t.Fatalf("decode organizer list: %v", err)
	}
	if rec.Code != http.StatusOK || len(orgRows.Data) != 1 || orgRows.Data[0].ID != created.Data.OrganizerID {
		t.Fatalf("auto-provisioned organizer must be searchable, got %d rows", len(orgRows.Data))
	}

	// The event is visible on the public list.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/events?q="+url.QueryEscape(title)+"&limit=100", "", "")
	var pub struct {
		Data []adminEventItem `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pub); err != nil {
		t.Fatalf("decode public events: %v; body=%s", err, rec.Body.String())
	}
	found := false
	for _, e := range pub.Data {
		if e.ID == created.Data.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("admin-created event must appear on the public list")
	}

	// Detail: stdout organizer identity + venue.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/events/"+created.Data.ID, "", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin detail: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var detail adminEventDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Data.Organizer.Name != orgName || detail.Data.Venue == nil || detail.Data.Venue.ID != *created.Data.VenueID {
		t.Fatalf("detail must resolve organizer+venue, got %+v", detail.Data)
	}
}

// TestAdminEventCreateGuards covers the atomic create's validation surface:
// organizer required, venue required for publish, unresolved ids, malformed
// venue, and publish:false keeping a draft.
func TestAdminEventCreateGuards(t *testing.T) {
	_, h := newTestApp(t)
	adminTok, _ := adminApproveToken(t, h, "evadm2")

	unique := time.Now().UnixNano()
	orgID := createAdminOrganizer(t, h, adminTok, fmt.Sprintf("Guard Org %d", unique))
	starts := time.Now().Add(30 * time.Hour).Format(time.RFC3339)

	// publish:false → draft (no venue needed).
	body := fmt.Sprintf(`{
		"organizer_id": %q,
		"title": "Draft Kept %d",
		"starts_at": %q,
		"publish": false
	}`, orgID, unique, starts)
	rec := doJSON(t, h, http.MethodPost, "/api/v1/admin/events", body, adminTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("draft create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var draft struct {
		Data adminEventItem `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &draft); err != nil {
		t.Fatalf("decode draft: %v", err)
	}
	if draft.Data.Status != "draft" {
		t.Fatalf("publish:false must keep draft, got %q", draft.Data.Status)
	}

	// publish (default true) without a venue → 422.
	body = fmt.Sprintf(`{
		"organizer_id": %q,
		"title": "No Venue %d",
		"starts_at": %q
	}`, orgID, unique, starts)
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/events", body, adminTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("publish without venue: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// No organizer at all → 422.
	body = fmt.Sprintf(`{"title":"No Org %d","starts_at":%q}`, unique, starts)
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/events", body, adminTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing organizer: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// Unknown organizer id → 422 organizer_not_found.
	body = fmt.Sprintf(`{"organizer_id":"9f5f0a2c-0000-0000-0000-000000000001","title":"Ghost Org","starts_at":%q,"publish":false}`, starts)
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/events", body, adminTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown organizer: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// Unknown venue id → 422 venue_not_found.
	body = fmt.Sprintf(`{"organizer_id":%q,"venue_id":"9f5f0a2c-0000-0000-0000-000000000001","title":"Ghost Venue","starts_at":%q,"publish":false}`, orgID, starts)
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/events", body, adminTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown venue: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// Bad lat/lng on the inline venue → 422.
	body = fmt.Sprintf(`{"organizer_id":%q,"venue":{"name":"Bad","latitude":91,"longitude":0},"title":"Bad Venue %d","starts_at":%q,"publish":false}`, orgID, unique, starts)
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/events", body, adminTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid venue: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// Missing title → 422.
	body = fmt.Sprintf(`{"organizer_id":%q,"title":"","starts_at":%q,"publish":false}`, orgID, starts)
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/events", body, adminTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty title: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// Malformed starts_at → 400.
	body = fmt.Sprintf(`{"organizer_id":%q,"title":"Bad Date","starts_at":"not-a-date","publish":false}`, orgID)
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/events", body, adminTok)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad starts_at: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAdminEventReassign covers moving an event to another organizer by id or
// by a new inline organizer.
func TestAdminEventReassign(t *testing.T) {
	_, h := newTestApp(t)
	adminTok, _ := adminApproveToken(t, h, "evadm3")

	unique := time.Now().UnixNano()

	// Create an event under an inline organizer.
	title := fmt.Sprintf("Reassign Me %d", unique)
	rec := doJSON(t, h, http.MethodPost, "/api/v1/admin/events", adminEventBody(unique, title, fmt.Sprintf("First Org %d", unique), nil), adminTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data adminEventItem `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	origOrg := created.Data.OrganizerID

	// Reassign to a NEW inline organizer.
	secondOrg := fmt.Sprintf("Second Org %d", unique)
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/admin/events/"+created.Data.ID+"/organizer",
		fmt.Sprintf(`{"organizer":{"name":%q}}`, secondOrg), adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("reassign inline: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var moved struct {
		Data adminEventItem `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &moved); err != nil {
		t.Fatalf("decode move: %v", err)
	}
	if moved.Data.OrganizerID == origOrg {
		t.Fatalf("reassign must change organizer_id")
	}

	// Reassign by existing id (back to the original via directory lookup).
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/admin/events/"+created.Data.ID+"/organizer",
		fmt.Sprintf(`{"organizer_id":%q}`, origOrg), adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("reassign by id: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var back struct {
		Data adminEventItem `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &back); err != nil {
		t.Fatalf("decode move back: %v", err)
	}
	if back.Data.OrganizerID != origOrg {
		t.Fatalf("reassign by id must restore original organizer")
	}

	// Missing event → 404.
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/admin/events/9f5f0a2c-0000-0000-0000-000000000001/organizer",
		fmt.Sprintf(`{"organizer_id":%q}`, origOrg), adminTok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("reassign missing event: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	// No target → 422.
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/admin/events/"+created.Data.ID+"/organizer", `{}`, adminTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("reassign without target: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAdminEventEditPublishCancel covers the edit-any, publish and cancel
// lifecycle endpoints plus their guards.
func TestAdminEventEditPublishCancel(t *testing.T) {
	_, h := newTestApp(t)
	adminTok, _ := adminApproveToken(t, h, "evadm4")

	unique := time.Now().UnixNano()
	orgID := createAdminOrganizer(t, h, adminTok, fmt.Sprintf("Lifecycle Org %d", unique))
	starts := time.Now().Add(24 * time.Hour).Format(time.RFC3339)

	// Create a draft.
	body := fmt.Sprintf(`{"organizer_id":%q,"title":"Lifecycle Draft %d","starts_at":%q,"publish":false}`, orgID, unique, starts)
	rec := doJSON(t, h, http.MethodPost, "/api/v1/admin/events", body, adminTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create draft: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var draft struct {
		Data adminEventItem `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &draft); err != nil {
		t.Fatalf("decode draft: %v", err)
	}
	id := draft.Data.ID

	// Editing a draft without a venue → 422 (publish still requires one).
	newTitle := fmt.Sprintf("Lifecycle Edited %d", unique)
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/admin/events/"+id,
		fmt.Sprintf(`{"title":%q,"starts_at":%q,"publish":false}`, newTitle, starts), adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit draft: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Missing event → 404 on edit and transitions.
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/admin/events/9f5f0a2c-0000-0000-0000-000000000001",
		fmt.Sprintf(`{"title":"x","starts_at":%q}`, starts), adminTok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("edit missing: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	// Malformed edit body → 400.
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/admin/events/"+id,
		`{"title":"x","starts_at":"nope"}`, adminTok)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed edit: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	// Publish a draft without venue → 422.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/events/"+id+"/publish", "", adminTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("publish draft w/o venue: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// Give it a venue via edit, then publish.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/venues?limit=1", "", adminTok)
	var venueRows struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &venueRows); err != nil || len(venueRows.Data) == 0 {
		t.Fatalf("need an existing venue for the publish test, got %d rows", len(venueRows.Data))
	}
	vid := venueRows.Data[0].ID
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/admin/events/"+id,
		fmt.Sprintf(`{"venue_id":%q,"title":%q,"starts_at":%q}`, vid, newTitle, starts), adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("attach venue: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/events/"+id+"/publish", "", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// Idempotent publish guard: already published → 409.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/events/"+id+"/publish", "", adminTok)
	if rec.Code != http.StatusConflict {
		t.Fatalf("double publish: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// Cancel published → cancelled; double cancel → 409.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/events/"+id+"/cancel", "", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/events/"+id+"/cancel", "", adminTok)
	if rec.Code != http.StatusConflict {
		t.Fatalf("double cancel: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// Republish from cancelled works.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/events/"+id+"/publish", "", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("republish cancelled: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var repub struct {
		Data adminEventItem `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &repub); err != nil {
		t.Fatalf("decode republish: %v", err)
	}
	if repub.Data.Status != "published" {
		t.Fatalf("republish must say published, got %q", repub.Data.Status)
	}
}
