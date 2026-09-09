package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// rsvpState mirrors the per-event RSVP state payload.
type rsvpState struct {
	Going   bool   `json:"going"`
	Status  string `json:"status"`
	Created string `json:"created_at"`
}

func getRsvpState(t *testing.T, h http.Handler, eventID, token string) rsvpState {
	t.Helper()
	rec := doJSON(t, h, http.MethodGet, "/api/v1/events/"+eventID+"/rsvp", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("rsvp state: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data rsvpState `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode rsvp state: %v; body=%s", err, rec.Body.String())
	}
	return resp.Data
}

// createPublishedEventWithCapacity creates a venue + published event limited to
// `capacity` seats (max_attendees), returning the event id.
func createPublishedEventWithCapacity(t *testing.T, h http.Handler, orgTok, start string, capacity int) string {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/api/v1/venues",
		fmt.Sprintf(`{"name":"Venue %d","latitude":40.71,"longitude":-74.00,"city":"NYC"}`, time.Now().UnixNano()), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create venue: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	venueID := idFromCreate(t, rec, "id")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events",
		fmt.Sprintf(`{"title":"Event %d","starts_at":%q,"venue_id":%q,"price_is_free":true,"max_attendees":%d}`,
			time.Now().UnixNano(), start, venueID, capacity), orgTok)
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

// TestRsvpLifecycle covers the RSVP toggle: go, duplicate (409), cancel, and
// re-go, plus the my-RSVPs list and the published-only gate.
func TestRsvpLifecycle(t *testing.T) {
	_, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "rsvporg", fmt.Sprintf("Rsvp Org %d", time.Now().UnixNano()))
	eventID := createPublishedEvent(t, h, orgTok, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
	_, userA, _ := registerAndToken(t, h, "rsvpa")
	_, userB, _ := registerAndToken(t, h, "rsvpb")

	// Empty body RSVP defaults to public.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/rsvp", "", userA)
	if rec.Code != http.StatusOK {
		t.Fatalf("rsvp: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if st := getRsvpState(t, h, eventID, userA); !st.Going {
		t.Fatalf("expected going=true after RSVP, got %+v", st)
	}

	// Duplicate RSVP is a 409.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/rsvp", `{}`, userA)
	if rec.Code != http.StatusConflict || !jsonContains(rec.Body.String(), "duplicate_rsvp") {
		t.Fatalf("duplicate rsvp: expected 409 duplicate_rsvp, got %d: %s", rec.Code, rec.Body.String())
	}

	// userB RSVPs private.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/rsvp", `{"public_rsvp":false}`, userB)
	if rec.Code != http.StatusOK {
		t.Fatalf("private rsvp: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// My-RSVPs list shows both with event summaries and visibility flags.
	for _, tc := range []struct {
		token       string
		wantHowMany int
		wantPublic  []bool
	}{
		{userA, 1, []bool{true}},
		{userB, 1, []bool{false}},
	} {
		rec = doJSON(t, h, http.MethodGet, "/api/v1/me/rsvps", "", tc.token)
		if rec.Code != http.StatusOK {
			t.Fatalf("my rsvps: expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var list struct {
			Data []struct {
				EventID    string `json:"event_id"`
				Status     string `json:"status"`
				PublicRsvp bool   `json:"public_rsvp"`
				Event      struct {
					Title string `json:"title"`
				} `json:"event"`
			} `json:"data"`
			Pagination struct {
				Total int `json:"total"`
			} `json:"pagination"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decode my rsvps: %v; body=%s", err, rec.Body.String())
		}
		if list.Pagination.Total != tc.wantHowMany || len(list.Data) != tc.wantHowMany {
			t.Fatalf("expected %d rsvp, got total=%d items=%d", tc.wantHowMany, list.Pagination.Total, len(list.Data))
		}
		if list.Data[0].EventID != eventID || list.Data[0].Status != "confirmed" {
			t.Fatalf("unexpected rsvp item: %+v", list.Data[0])
		}
		if list.Data[0].PublicRsvp != tc.wantPublic[0] {
			t.Fatalf("expected public_rsvp=%v, got %+v", tc.wantPublic[0], list.Data[0])
		}
		if list.Data[0].Event.Title == "" {
			t.Fatalf("expected event summary, got %+v", list.Data[0])
		}
	}

	// Cancel releases the seat and state flips to not going; then re-go works.
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/events/"+eventID+"/rsvp", "", userB)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel rsvp: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if st := getRsvpState(t, h, eventID, userB); st.Going {
		t.Fatalf("expected going=false after cancel, got %+v", st)
	}
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/rsvp", `{}`, userB)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-rsvp after cancel: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/events/"+eventID+"/rsvp", "", userB)
	if rec.Code != http.StatusOK {
		t.Fatalf("second cancel: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// RSVP on a draft event is a 404 (not publicly visible).
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events",
		fmt.Sprintf(`{"title":"Draft %d","starts_at":%q,"price_is_free":true}`, time.Now().UnixNano(), time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339)), orgTok)
	draftID := idFromCreate(t, rec, "id")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+draftID+"/rsvp", `{}`, userA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("rsvp draft event: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	// Anonymous RSVP is rejected.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/rsvp", `{}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous rsvp: expected 401, got %d", rec.Code)
	}
}

// TestRsvpCapacityAndConcurrency verifies max_attendees enforcement: seats are
// reserved atomically under a row lock, so concurrent RSVPs cannot oversell.
// Each subtest uses its own app instance because the register endpoint shares
// an in-memory IP rate limit (~5 per app).
func TestRsvpCapacityAndConcurrency(t *testing.T) {
	app, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "caporg", fmt.Sprintf("Cap Org %d", time.Now().UnixNano()))
	_ = app

	t.Run("sequential capacity", func(t *testing.T) {
		_, h2 := newTestApp(t)
		_, capA, _ := registerAndToken(t, h2, "capa")
		_, capB, _ := registerAndToken(t, h2, "capb")
		eventID := createPublishedEventWithCapacity(t, h2, orgTok, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339), 1)

		if rec := doJSON(t, h2, http.MethodPost, "/api/v1/events/"+eventID+"/rsvp", `{}`, capA); rec.Code != http.StatusOK {
			t.Fatalf("rsvp: expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		rec := doJSON(t, h2, http.MethodPost, "/api/v1/events/"+eventID+"/rsvp", `{}`, capB)
		if rec.Code != http.StatusConflict || !jsonContains(rec.Body.String(), "event_full") {
			t.Fatalf("second rsvp at capacity: expected 409 event_full, got %d: %s", rec.Code, rec.Body.String())
		}
		// Cancelling frees the seat.
		if rec := doJSON(t, h2, http.MethodDelete, "/api/v1/events/"+eventID+"/rsvp", "", capA); rec.Code != http.StatusOK {
			t.Fatalf("cancel: expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if rec := doJSON(t, h2, http.MethodPost, "/api/v1/events/"+eventID+"/rsvp", `{}`, capB); rec.Code != http.StatusOK {
			t.Fatalf("rsvp after cancel: expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("concurrent no oversell", func(t *testing.T) {
		_, h3 := newTestApp(t)
		var toks []string
		for _, p := range []string{"cona", "conb", "conc", "cond"} {
			_, tok, _ := registerAndToken(t, h3, p)
			toks = append(toks, tok)
		}
		eventID := createPublishedEventWithCapacity(t, h3, orgTok, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339), 2)
		var wg sync.WaitGroup
		codes := make([]int, len(toks))
		for i, tok := range toks {
			wg.Add(1)
			go func(i int, tok string) {
				defer wg.Done()
				codes[i] = doJSON(t, h3, http.MethodPost, "/api/v1/events/"+eventID+"/rsvp", `{}`, tok).Code
			}(i, tok)
		}
		wg.Wait()

		okays, fulls := 0, 0
		for _, c := range codes {
			switch c {
			case http.StatusOK:
				okays++
			case http.StatusConflict:
				fulls++
			default:
				t.Fatalf("unexpected rsvp status %d", c)
			}
		}
		if okays != 2 {
			t.Fatalf("expected exactly 2 RSVPs to succeed under capacity 2, got %d (statuses=%v)", okays, codes)
		}
		if fulls != 2 {
			t.Fatalf("expected 2 capacity rejections, got %d (statuses=%v)", fulls, codes)
		}
	})
}

// TestReviews covers the review lifecycle and its attendance gate.
func TestReviews(t *testing.T) {
	_, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "revorg", fmt.Sprintf("Rev Org %d", time.Now().UnixNano()))
	// A past event (started) is reviewable after an RSVP; a future one is not.
	pastID := createPublishedEvent(t, h, orgTok, time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339))
	futureID := createPublishedEvent(t, h, orgTok, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
	_, userA, _ := registerAndToken(t, h, "reva")
	_, userB, _ := registerAndToken(t, h, "revb")

	// Reviewing a future event is refused even with an RSVP.
	doJSON(t, h, http.MethodPost, "/api/v1/events/"+futureID+"/rsvp", `{}`, userA)
	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+futureID+"/reviews", `{"rating":5,"body":"soon"}`, userA)
	if rec.Code != http.StatusForbidden || !jsonContains(rec.Body.String(), "review_not_allowed") {
		t.Fatalf("future review: expected 403 review_not_allowed, got %d: %s", rec.Code, rec.Body.String())
	}

	// Reviewing without an RSVP is refused.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+pastID+"/reviews", `{"rating":4,"body":"no attendance"}`, userB)
	if rec.Code != http.StatusForbidden || !jsonContains(rec.Body.String(), "review_not_allowed") {
		t.Fatalf("unattended review: expected 403 review_not_allowed, got %d: %s", rec.Code, rec.Body.String())
	}

	// Validation: rating outside 1..5 → 422.
	doJSON(t, h, http.MethodPost, "/api/v1/events/"+pastID+"/rsvp", `{}`, userA)
	for _, bad := range []string{`{"rating":0}`, `{"rating":6}`, `{"body":""}`} {
		rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+pastID+"/reviews", bad, userA)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("bad review %s: expected 422, got %d: %s", bad, rec.Code, rec.Body.String())
		}
	}

	// Happy path: 201, appears in the public list.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+pastID+"/reviews", `{"rating":5,"body":"Great night"}`, userA)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create review: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct {
			ID     string `json:"id"`
			Rating int16  `json:"rating"`
			Body   string `json:"body"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode review: %v; body=%s", err, rec.Body.String())
	}
	if created.Data.Rating != 5 || created.Data.Body != "Great night" {
		t.Fatalf("unexpected review payload: %+v", created.Data)
	}

	rec = doJSON(t, h, http.MethodGet, "/api/v1/events/"+pastID+"/reviews", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list reviews: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode review list: %v; body=%s", err, rec.Body.String())
	}
	if list.Pagination.Total != 1 {
		t.Fatalf("expected 1 published review, got total=%d (%s)", list.Pagination.Total, rec.Body.String())
	}

	// Duplicate review → 409.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+pastID+"/reviews", `{"rating":3}`, userA)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate review: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// Owner edits their own review.
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/reviews/"+created.Data.ID, `{"rating":2,"body":"Revised"}`, userA)
	if rec.Code != http.StatusOK {
		t.Fatalf("update review: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// userB cannot edit or delete userA's review.
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/reviews/"+created.Data.ID, `{"rating":1}`, userB)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-user review update: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/reviews/"+created.Data.ID, "", userB)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-user review delete: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	// Owner deletes (soft) → the public list empties.
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/reviews/"+created.Data.ID, "", userA)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete review: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/events/"+pastID+"/reviews", "", "")
	if rec.Code != http.StatusOK || !jsonContains(rec.Body.String(), `"total":0`) {
		t.Fatalf("expected empty review list after delete, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestReports covers filing reports against each target type and the
// under_review moderation transition (visibility is preserved).
func TestReports(t *testing.T) {
	_, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "reportorg", fmt.Sprintf("Report Org %d", time.Now().UnixNano()))
	eventID := createPublishedEvent(t, h, orgTok, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))

	// A venue + a comment target. The venue name sorts first so the paginated
	// venues list (ordered by name) shows it on page 1.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/venues",
		fmt.Sprintf(`{"name":"Aaa Venue %d","latitude":40.71,"longitude":-74.00,"city":"NYC"}`, time.Now().UnixNano()), orgTok)
	venueID := idFromCreate(t, rec, "id")
	_, userA, _ := registerAndToken(t, h, "repa")
	_, userB, _ := registerAndToken(t, h, "repb")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/comments", `{"body":"suspicious"}`, userA)
	commentID := idFromCreate(t, rec, "id")

	// Report the event: 201, moderation flips to under_review, still visible.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/report", `{"reason_code":"spam"}`, userB)
	if rec.Code != http.StatusCreated {
		t.Fatalf("report event: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/events/"+eventID, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("event after report: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var event struct {
		Data struct {
			ModerationStatus string `json:"moderation_status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &event); err != nil {
		t.Fatalf("decode event detail: %v", err)
	}
	if event.Data.ModerationStatus != "under_review" {
		t.Fatalf("expected event moderation under_review, got %q", event.Data.ModerationStatus)
	}

	// Report the venue: 201, and the venue remains listed (active).
	rec = doJSON(t, h, http.MethodPost, "/api/v1/venues/"+venueID+"/report", `{"reason_code":"inaccurate"}`, userB)
	if rec.Code != http.StatusCreated {
		t.Fatalf("report venue: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/venues", "", "")
	if rec.Code != http.StatusOK || !jsonContains(rec.Body.String(), venueID) || jsonContains(rec.Body.String(), `"total":0`) {
		t.Fatalf("venue after report: expected still listed, got %d: %s", rec.Code, rec.Body.String())
	}

	// Report the comment: 201, comment still readable (under_review not hidden).
	rec = doJSON(t, h, http.MethodPost, "/api/v1/comments/"+commentID+"/report", `{"reason_code":"harassment","description":"nasty"}`, userB)
	if rec.Code != http.StatusCreated {
		t.Fatalf("report comment: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/events/"+eventID+"/comments", "", "")
	if rec.Code != http.StatusOK || !jsonContains(rec.Body.String(), commentID) {
		t.Fatalf("comment after report: expected still listed, got %d: %s", rec.Code, rec.Body.String())
	}

	// Report a user: recorded as-is (users are own-row RLS, so no target check).
	rec = doJSON(t, h, http.MethodPost, "/api/v1/users/00000000-0000-0000-0000-000000000099/report", `{"reason_code":"other"}`, userB)
	if rec.Code != http.StatusCreated {
		t.Fatalf("report user: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Validation: missing reason_code → 422; draft target → 404; anon → 401.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/report", `{"description":"no code"}`, userB)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("report without reason: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events",
		fmt.Sprintf(`{"title":"Draft %d","starts_at":%q,"price_is_free":true}`, time.Now().UnixNano(), time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339)), orgTok)
	draftID := idFromCreate(t, rec, "id")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+draftID+"/report", `{"reason_code":"spam"}`, userB)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("report draft event: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/report", `{"reason_code":"spam"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous report: expected 401, got %d", rec.Code)
	}
}

func jsonContains(s, sub string) bool {
	return strings.Contains(s, sub)
}
