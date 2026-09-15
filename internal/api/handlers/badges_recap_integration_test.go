package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// setupBadgesRecap creates an organizer, a published event, and an ordinary
// user. Returns the handler, organizer token, user token, and event id.
func setupBadgesRecap(t *testing.T) (h http.Handler, orgTok, userTok, eventID string) {
	t.Helper()
	t.Setenv("AUTH_REGISTER_RATE_LIMIT", "100")
	_, h = newTestApp(t)
	orgTok, _ = promoteOrganizer(t, h, "brg", fmt.Sprintf("Badges Org %d", time.Now().UnixNano()))
	eventID = createPublishedEvent(t, h, orgTok, time.Now().Add(time.Hour).Format(time.RFC3339))
	_, userTok, _ = registerAndToken(t, h, "use")
	return h, orgTok, userTok, eventID
}

// badgeListBody models the {"data": [...]} envelope of GET /users/me/badges.
type badgeListBody struct {
	Data []map[string]any `json:"data"`
}

func getBadges(t *testing.T, h http.Handler, token string) []map[string]any {
	t.Helper()
	rec := doJSON(t, h, http.MethodGet, "/api/v1/users/me/badges", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("list badges: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body badgeListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode badges: %v (body %s)", err, rec.Body.String())
	}
	return body.Data
}

// recap is the recap dashboard shape (inside the {"data": ...} envelope).
type recap struct {
	MomentCount   int               `json:"moment_count"`
	TopMoments    []json.RawMessage `json:"top_moments"`
	AttendeeCount int               `json:"attendee_count"`
	ReviewCount   int               `json:"review_count"`
	SessionCount  int               `json:"session_count"`
	Cached        bool              `json:"cached"`
}

type recapBody struct {
	Data recap `json:"data"`
}

func getRecap(t *testing.T, h http.Handler, eventID, token string) recap {
	t.Helper()
	rec := doJSON(t, h, http.MethodGet, "/api/v1/events/"+eventID+"/recap", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("recap: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body recapBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode recap: %v (body %s)", err, rec.Body.String())
	}
	return body.Data
}

// TestBadgeAwardedExactlyOnceOnRSVP walks the badge lifecycle through the API:
// badges are protected, an empty list initially, a first RSVP awards
// first_rsvp, and a second RSVP does not duplicate it.
func TestBadgeAwardedExactlyOnceOnRSVP(t *testing.T) {
	h, orgTok, userTok, eventID := setupBadgesRecap(t)

	// Badges are protected: anonymous reads are rejected.
	rec := doJSON(t, h, http.MethodGet, "/api/v1/users/me/badges", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous badges: expected 401, got %d: %s", rec.Code, rec.Body.String())
	}

	// No badges yet.
	if badges := getBadges(t, h, userTok); len(badges) != 0 {
		t.Fatalf("expected an empty badge list, got %+v", badges)
	}

	// First RSVP earns first_rsvp.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/rsvp", "", userTok)
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("rsvp: expected 2xx, got %d: %s", rec.Code, rec.Body.String())
	}
	badges := getBadges(t, h, userTok)
	if len(badges) != 1 {
		t.Fatalf("expected exactly one badge after first RSVP, got %+v", badges)
	}
	if badges[0]["badge_type"] != "first_rsvp" {
		t.Fatalf("expected badge_type=first_rsvp, got %+v", badges[0])
	}
	if md, ok := badges[0]["metadata"].(map[string]any); !ok || md["event_id"] != eventID {
		t.Fatalf("expected metadata.event_id, got %+v", badges[0]["metadata"])
	}

	// A second RSVP on a different event must not duplicate the badge.
	secondEvent := createPublishedEvent(t, h, orgTok, time.Now().Add(time.Hour).Format(time.RFC3339))
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+secondEvent+"/rsvp", "", userTok)
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("second rsvp: %d: %s", rec.Code, rec.Body.String())
	}
	if badges := getBadges(t, h, userTok); len(badges) != 1 {
		t.Fatalf("badge must be exactly-once, got %d badges", len(badges))
	}

	// Another user has no badges of their own.
	_, otherTok, _ := registerAndToken(t, h, "ane")
	if badges := getBadges(t, h, otherTok); len(badges) != 0 {
		t.Fatalf("unrelated user must have no badges, got %+v", badges)
	}
}

// TestRecapPublicAndCacheFlag: the recap is publicly readable, regenerates on
// first read, is served from cache on a quiet second read, and regenerates
// when a schedule slot is added.
func TestRecapPublicAndCacheFlag(t *testing.T) {
	h, orgTok, _, eventID := setupBadgesRecap(t)

	// Anonymous first read: regenerated, empty event.
	r1 := getRecap(t, h, eventID, "")
	if r1.Cached || r1.MomentCount != 0 || r1.AttendeeCount != 0 || r1.SessionCount != 0 {
		t.Fatalf("first recap must regenerate an empty refresh, got %+v", r1)
	}

	// Quiet second read: served from cache.
	if r2 := getRecap(t, h, eventID, ""); !r2.Cached {
		t.Fatalf("second recap must be cached, got %+v", r2)
	}

	// Organizer adds a schedule slot → recap regenerates with one session.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/schedule",
		fmt.Sprintf(`{"title":"Keynote","starts_at":%q}`, time.Now().Add(2*time.Hour).Format(time.RFC3339)), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create session: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	r3 := getRecap(t, h, eventID, "")
	if r3.Cached || r3.SessionCount != 1 || len(r3.TopMoments) != 0 {
		t.Fatalf("recap must regenerate after a session is added, got %+v", r3)
	}

	// Invisible/invalid events never surface a recap.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/events/not-a-uuid/recap", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-uuid recap: expected 404, got %d", rec.Code)
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/events/00000000-0000-0000-0000-000000000000/recap", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown recap: expected 404, got %d", rec.Code)
	}
}
