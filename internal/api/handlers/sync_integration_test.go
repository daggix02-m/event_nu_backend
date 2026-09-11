package handlers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// syncChangeWire mirrors the wire shape of one sync delta entry.
type syncChangeWire struct {
	Domain  string          `json:"domain"`
	Op      string          `json:"op"`
	ID      string          `json:"id"`
	Payload json.RawMessage `json:"payload"`
}

// syncWire mirrors the sync response body.
type syncWire struct {
	Changes    []syncChangeWire `json:"changes"`
	NextCursor string           `json:"next_cursor"`
	HasMore    map[string]bool  `json:"has_more"`
}

func getSync(t *testing.T, h http.Handler, queryPath, token string) syncWire {
	t.Helper()
	rec := doJSON(t, h, http.MethodGet, "/api/v1/sync"+queryPath, "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data syncWire `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode sync response: %v; body=%s", err, rec.Body.String())
	}
	return env.Data
}

func syncHas(w syncWire, domain, op, id string) bool {
	for _, c := range w.Changes {
		if c.Domain == domain && c.Op == op && c.ID == id {
			return true
		}
	}
	return false
}

// cur builds the query string for a sync poll at the given cursor.
func cur(cursor time.Time) string {
	return "?cursor=" + url.QueryEscape(cursor.Format(time.RFC3339Nano))
}

// createVenueAndEvent creates a venue + published event as the organizer,
// returning both ids.
func createVenueAndEvent(t *testing.T, h http.Handler, orgTok string) (venueID, eventID string) {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/api/v1/venues",
		fmt.Sprintf(`{"name":"Sync Venue %d","latitude":40.71,"longitude":-74.00,"city":"NYC"}`, time.Now().UnixNano()), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create venue: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	venueID = idFromCreate(t, rec, "id")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events",
		fmt.Sprintf(`{"title":"Delta Event %d","starts_at":%q,"venue_id":%q,"price_is_free":true}`,
			time.Now().UnixNano(), time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339), venueID), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create event: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	eventID = idFromCreate(t, rec, "id")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/publish", "", orgTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish event: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	return venueID, eventID
}

func createCommentOnEvent(t *testing.T, h http.Handler, eventID, token string) string {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/comments",
		fmt.Sprintf(`{"body":"sync comment %d"}`, time.Now().UnixNano()), token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	return idFromCreate(t, rec, "id")
}

func TestSyncRequiresAuth(t *testing.T) {
	_, h := newTestApp(t)
	rec := doJSON(t, h, http.MethodGet, "/api/v1/sync", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("sync without token: expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSyncParamValidation(t *testing.T) {
	_, h := newTestApp(t)
	_, tok, _ := registerAndToken(t, h, "synv")

	rec := doJSON(t, h, http.MethodGet, "/api/v1/sync?domains=categories", "", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid poll: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	for _, q := range []string{
		"?cursor=not-a-timestamp",
		"?domains=events,bogus",
		"?limit=0",
		"?limit=999999",
		"?domains=comments&limit=abc",
	} {
		rec := doJSON(t, h, http.MethodGet, "/api/v1/sync"+q, "", tok)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("poll %s: expected 400, got %d: %s", q, rec.Code, rec.Body.String())
		}
	}
}

// TestSyncDeltaFlow: rows created after a cursor appear as upserts, polls with
// a fresh cursor only return new rows, and cursors are monotonic.
func TestSyncDeltaFlow(t *testing.T) {
	_, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "syncf", fmt.Sprintf("Sync Flow Org %d", time.Now().UnixNano()))
	_, userTok, _ := registerAndToken(t, h, "syncu")

	marker := time.Now().UTC()
	venueID, eventID := createVenueAndEvent(t, h, orgTok)
	commentID := createCommentOnEvent(t, h, eventID, userTok)

	w1 := getSync(t, h, cur(marker), userTok)
	if !syncHas(w1, "events", "upsert", eventID) {
		t.Fatalf("expected event upsert in delta: %+v", w1.Changes)
	}
	if !syncHas(w1, "venues", "upsert", venueID) {
		t.Fatalf("expected venue upsert in delta: %+v", w1.Changes)
	}
	if !syncHas(w1, "comments", "upsert", commentID) {
		t.Fatalf("expected comment upsert in delta: %+v", w1.Changes)
	}

	next, err := time.Parse(time.RFC3339Nano, w1.NextCursor)
	if err != nil || next.Before(marker) {
		t.Fatalf("next cursor %q not monotonic from marker", w1.NextCursor)
	}

	w2 := getSync(t, h, "?cursor="+url.QueryEscape(w1.NextCursor), userTok)
	second, err := time.Parse(time.RFC3339Nano, w2.NextCursor)
	if err != nil || second.Before(next) {
		t.Fatalf("next cursor regressed: %q after %q", w2.NextCursor, w1.NextCursor)
	}

	// The cursor is inclusive, so polling at w1.NextCursor re-emits boundary
	// rows (updated_at == cursor). Poll strictly past it to prove only rows
	// genuinely newer than the watermark come back.
	strict := next.Add(time.Microsecond).UTC().Format(time.RFC3339Nano)
	commentID2 := createCommentOnEvent(t, h, eventID, userTok)
	w3 := getSync(t, h, "?cursor="+url.QueryEscape(strict), userTok)
	if !syncHas(w3, "comments", "upsert", commentID2) {
		t.Fatalf("expected new comment upsert in delta: %+v", w3.Changes)
	}
	if syncHas(w3, "comments", "upsert", commentID) {
		t.Fatalf("old comment re-emitted past the watermark: %+v", w3.Changes)
	}
}

// TestSyncPaginationTerminates: with limit=1 over a multi-row events window the
// client can loop cursor -> response until has_more disappears, seeing every
// id exactly-once-or-more, with no infinite non-progressing pages.
func TestSyncPaginationTerminates(t *testing.T) {
	_, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "syncp", fmt.Sprintf("Sync Page Org %d", time.Now().UnixNano()))
	_, userTok, _ := registerAndToken(t, h, "syncq")

	marker := time.Now().UTC()
	want := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		want = append(want, createPublishedEvent(t, h, orgTok, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339)))
	}

	cursor := marker.Format(time.RFC3339Nano)
	seen := map[string]bool{}
	last, _ := time.Parse(time.RFC3339Nano, cursor)
	for round := 0; round < 30; round++ {
		w := getSync(t, h, "?cursor="+url.QueryEscape(cursor)+"&domains=events&limit=1", userTok)
		for _, c := range w.Changes {
			if c.Domain == "events" && c.Op == "upsert" {
				seen[c.ID] = true
			}
		}
		parsed, err := time.Parse(time.RFC3339Nano, w.NextCursor)
		if err != nil || parsed.Before(last) {
			t.Fatalf("next cursor regressed: %q after %q (round %d)", w.NextCursor, cursor, round)
		}
		last = parsed
		if !w.HasMore["events"] {
			cursor = w.NextCursor
			break
		}
		cursor = w.NextCursor
	}
	for _, id := range want {
		if !seen[id] {
			t.Fatalf("event %s never emitted across pagination (seen %v)", id, seen)
		}
	}
	// One more poll at the final cursor: the inclusive cursor may re-emit the
	// boundary row (updated_at == cursor) but must never produce unseen ids or
	// more pages to fetch.
	w := getSync(t, h, "?cursor="+url.QueryEscape(cursor)+"&domains=events&limit=1", userTok)
	for _, c := range w.Changes {
		if c.Op == "upsert" && !seen[c.ID] {
			t.Fatalf("terminal poll produced unseen event %s (seen %v)", c.ID, seen)
		}
	}
	if w.HasMore["events"] {
		t.Fatalf("terminal poll reported has_more: %+v", w.Changes)
	}
}

// TestSyncSoftDeleteOp: a deleted review surfaces as a delete op (id only) in
// the domain that soft deletes, and never as an upsert afterwards.
func TestSyncSoftDeleteOp(t *testing.T) {
	_, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "syncd", fmt.Sprintf("Sync Del Org %d", time.Now().UnixNano()))
	pastID := createPublishedEvent(t, h, orgTok, time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339))
	_, revTok, _ := registerAndToken(t, h, "syncr")

	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+pastID+"/rsvp", `{}`, revTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("rsvp: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	marker := time.Now().UTC()
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+pastID+"/reviews", `{"rating":5,"body":"great sync"}`, revTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create review: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	reviewID := idFromCreate(t, rec, "id")

	w1 := getSync(t, h, cur(marker), revTok)
	if !syncHas(w1, "reviews", "upsert", reviewID) {
		t.Fatalf("expected review upsert in delta: %+v", w1.Changes)
	}

	rec = doJSON(t, h, http.MethodDelete, "/api/v1/reviews/"+reviewID, "", revTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete review: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	w2 := getSync(t, h, "?cursor="+url.QueryEscape(w1.NextCursor), revTok)
	if !syncHas(w2, "reviews", "delete", reviewID) {
		t.Fatalf("expected review delete op in delta: %+v", w2.Changes)
	}
	for _, c := range w2.Changes {
		if c.Domain == "reviews" && c.Op == "delete" && c.ID == reviewID {
			if !bytes.Equal(c.Payload, []byte("null")) {
				t.Fatalf("delete op must carry a null payload, got %s", string(c.Payload))
			}
		}
		if c.Domain == "reviews" && c.Op == "upsert" && c.ID == reviewID {
			t.Fatalf("deleted review must not be upserted: %+v", w2.Changes)
		}
	}
}
