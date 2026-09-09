package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"strings"
)

// promoteOrganizerWithID promotes an organizer and returns the organizer id
// captured from the approve response, along with the organizer's token.
func promoteOrganizerWithID(t *testing.T, h http.Handler, prefix, name string) (string, string) {
	t.Helper()
	_, orgTok, _ := registerAndToken(t, h, prefix)
	appID := applyForOrganizer(t, h, orgTok, name)
	adminTok, _ := adminApproveToken(t, h, prefix+"adm")
	rec := doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+appID+"/approve", `{}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve organizer: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			OrganizerID string `json:"organizer_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode approve response: %v; body=%s", err, rec.Body.String())
	}
	return resp.Data.OrganizerID, orgTok
}

// TestSaveFoldersAndSaves covers folder CRUD and the save/unsave lifecycle with
// ownership enforcement, per-user state on event detail, and the saves list.
func TestSaveFoldersAndSaves(t *testing.T) {
	_, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "saveorg", fmt.Sprintf("Save Org %d", time.Now().UnixNano()))
	eventID := createPublishedEvent(t, h, orgTok, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
	_, userA, _ := registerAndToken(t, h, "sava")
	_, userB, _ := registerAndToken(t, h, "savb")

	// Fresh user sees no folders.
	rec := doJSON(t, h, http.MethodGet, "/api/v1/me/save-folders", "", userA)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "id") {
		t.Fatalf("expected empty folder list, got %d: %s", rec.Code, rec.Body.String())
	}

	// Create a folder.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/me/save-folders", `{"name":"Concerts"}`, userA)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create folder: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var folder struct {
		Data struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &folder); err != nil {
		t.Fatalf("decode folder: %v", err)
	}
	if folder.Data.Name != "Concerts" {
		t.Fatalf("expected folder name, got %q", folder.Data.Name)
	}

	// Blank / oversized names are rejected.
	for _, bad := range []string{`{"name":"  "}`, fmt.Sprintf(`{"name":%q}`, strings.Repeat("x", 101))} {
		rec = doJSON(t, h, http.MethodPost, "/api/v1/me/save-folders", bad, userA)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("bad folder name: expected 422, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Save event into the folder.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/save",
		fmt.Sprintf(`{"folder_id":%q}`, folder.Data.ID), userA)
	if rec.Code != http.StatusOK {
		t.Fatalf("save event: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Event detail reflects per-user save state.
	assertEventSaveState(t, h, eventID, userA, true, folder.Data.ID)
	assertEventSaveState(t, h, eventID, "", false, "")

	// userB cannot save into userA's folder; under own-row RLS the folder is
	// invisible to userB, so this surfaces as 404 (like a non-existent folder).
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/save",
		fmt.Sprintf(`{"folder_id":%q}`, folder.Data.ID), userB)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-user folder save: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	// userB saves without a folder, then re-saves (idempotent), then unsaves.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/save", `{}`, userB)
	if rec.Code != http.StatusOK {
		t.Fatalf("save without folder: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/save", `{}`, userB)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-save: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/events/"+eventID+"/save", "", userB)
	if rec.Code != http.StatusOK {
		t.Fatalf("unsave: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/events/"+eventID+"/save", "", userB)
	if rec.Code != http.StatusOK {
		t.Fatalf("idempotent unsave: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	assertEventSaveState(t, h, eventID, userB, false, "")

	// The saves list shows userA's save with event summary + folder.
	var savedList struct {
		Data []struct {
			EventID  string  `json:"event_id"`
			FolderID *string `json:"folder_id"`
			Event    struct {
				Title string `json:"title"`
			} `json:"event"`
		} `json:"data"`
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/saves", "", userA)
	if rec.Code != http.StatusOK {
		t.Fatalf("list saves: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &savedList); err != nil {
		t.Fatalf("decode saves list: %v", err)
	}
	if len(savedList.Data) != 1 || savedList.Data[0].EventID != eventID {
		t.Fatalf("expected 1 save of our event, got %+v", savedList.Data)
	}
	if savedList.Data[0].FolderID == nil || *savedList.Data[0].FolderID != folder.Data.ID {
		t.Fatalf("expected folder_id on save, got %+v", savedList.Data[0])
	}
	if savedList.Data[0].Event.Title == "" {
		t.Fatalf("expected event summary title, got %+v", savedList.Data[0])
	}

	// userB's own list is empty (own-row RLS).
	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/saves", "", userB)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":0`) {
		t.Fatalf("expected empty save list for userB, got %d: %s", rec.Code, rec.Body.String())
	}

	// Rename the folder.
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/me/save-folders/"+folder.Data.ID, `{"name":"Live"}`, userA)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename folder: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Saving a draft event is refused (not publicly visible).
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events",
		fmt.Sprintf(`{"title":"Draft %d","starts_at":%q,"price_is_free":true}`, time.Now().UnixNano(), time.Now().Add(72*time.Hour).UTC().Format(time.RFC3339)), orgTok)
	draftID := idFromCreate(t, rec, "id")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+draftID+"/save", `{}`, userA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("save draft event: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	// Deleting the folder keeps the save, re-homing it to "no folder".
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/me/save-folders/"+folder.Data.ID, "", userA)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete folder: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/saves", "", userA)
	if rec.Code != http.StatusOK {
		t.Fatalf("list saves after folder delete: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &savedList); err != nil {
		t.Fatalf("decode saves list: %v", err)
	}
	if len(savedList.Data) != 1 || savedList.Data[0].FolderID != nil {
		t.Fatalf("expected save to survive folder delete with null folder, got %+v", savedList.Data)
	}

	// Anonymous save is rejected.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/save", `{}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous save: expected 401, got %d", rec.Code)
	}
}

// TestShareRecording covers share analytics: record, dedupe, validation, and
// the published-only gate.
func TestShareRecording(t *testing.T) {
	_, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "shareorg", fmt.Sprintf("Share Org %d", time.Now().UnixNano()))
	eventID := createPublishedEvent(t, h, orgTok, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
	_, userTok, _ := registerAndToken(t, h, "shareu")

	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/share",
		`{"channel":"whatsapp","ref":"home-feed"}`, userTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("record share: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	// A repeat share of the same (event, channel, ref) is deduplicated and safe.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/share",
		`{"channel":"whatsapp","ref":"home-feed"}`, userTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("repeat share: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Missing channel is invalid.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/share", `{"ref":"x"}`, userTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("share without channel: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// Sharing a draft event is refused.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events",
		fmt.Sprintf(`{"title":"Draft %d","starts_at":%q,"price_is_free":true}`, time.Now().UnixNano(), time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339)), orgTok)
	draftID := idFromCreate(t, rec, "id")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+draftID+"/share", `{"channel":"copy-link"}`, userTok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("share draft event: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	// Anonymous share is rejected.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/share", `{"channel":"x"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous share: expected 401, got %d", rec.Code)
	}
}

// TestFollowToggle covers organizer follows: idempotent follow/unfollow,
// follower counts, and per-user follow state on the public organizer profile.
func TestFollowToggle(t *testing.T) {
	_, h := newTestApp(t)
	orgID, _ := promoteOrganizerWithID(t, h, "followorg", fmt.Sprintf("Follow Org %d", time.Now().UnixNano()))
	_, userA, _ := registerAndToken(t, h, "fola")
	_, userB, _ := registerAndToken(t, h, "folb")

	// Anonymous organizer profile: public, count 0.
	rec := doJSON(t, h, http.MethodGet, "/api/v1/organizers/"+orgID, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("organizer profile: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	assertOrganizerFollowState(t, rec, 0, false, "")

	// userA follows (count 1), re-follow is a no-op, then userB (count 2).
	rec = doJSON(t, h, http.MethodPost, "/api/v1/organizers/"+orgID+"/follow", "", userA)
	assertOrganizerFollowState(t, rec, 1, true, "follow A")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/organizers/"+orgID+"/follow", "", userA)
	assertOrganizerFollowState(t, rec, 1, true, "re-follow A")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/organizers/"+orgID+"/follow", "", userB)
	assertOrganizerFollowState(t, rec, 2, true, "follow B")

	// Profile personalizes by reader: anonymous false, A true, B true.
	assertOrganizerDetail(t, h, orgID, "", 2, false)
	assertOrganizerDetail(t, h, orgID, userA, 2, true)
	assertOrganizerDetail(t, h, orgID, userB, 2, true)

	// userA unfollows (count 1); unfollow again is idempotent.
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/organizers/"+orgID+"/follow", "", userA)
	assertOrganizerFollowState(t, rec, 1, false, "unfollow A")
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/organizers/"+orgID+"/follow", "", userA)
	assertOrganizerFollowState(t, rec, 1, false, "idempotent unfollow A")
	assertOrganizerDetail(t, h, orgID, userA, 1, false)
	assertOrganizerDetail(t, h, orgID, userB, 1, true)

	// Following a non-existent organizer is a 404; anonymous follow is a 401.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/organizers/00000000-0000-0000-0000-000000000001/follow", "", userA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("follow unknown organizer: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodPost, "/api/v1/organizers/"+orgID+"/follow", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous follow: expected 401, got %d", rec.Code)
	}
}

func assertEventSaveState(t *testing.T, h http.Handler, eventID, token string, wantSaved bool, wantFolder string) {
	t.Helper()
	rec := doJSON(t, h, http.MethodGet, "/api/v1/events/"+eventID, "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("event detail: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			SavedByMe     bool    `json:"saved_by_me"`
			SavedFolderID *string `json:"saved_folder_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode event detail: %v; body=%s", err, rec.Body.String())
	}
	folderID := ""
	if resp.Data.SavedFolderID != nil {
		folderID = *resp.Data.SavedFolderID
	}
	if resp.Data.SavedByMe != wantSaved || folderID != wantFolder {
		t.Fatalf("expected saved_by_me=%v folder=%q, got saved=%v folder=%q", wantSaved, wantFolder, resp.Data.SavedByMe, folderID)
	}
}

func assertOrganizerDetail(t *testing.T, h http.Handler, orgID, token string, wantCount int, wantFollowed bool) {
	t.Helper()
	rec := doJSON(t, h, http.MethodGet, "/api/v1/organizers/"+orgID, "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("organizer detail: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	assertOrganizerFollowState(t, rec, wantCount, wantFollowed, "detail")
}

func assertOrganizerFollowState(t *testing.T, rec *httptest.ResponseRecorder, wantCount int, wantFollowed bool, ctx string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: expected 200, got %d: %s", ctx, rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			FollowerCount int  `json:"follower_count"`
			FollowedByMe  bool `json:"followed_by_me"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("%s: decode response: %v; body=%s", ctx, err, rec.Body.String())
	}
	if resp.Data.FollowerCount != wantCount || resp.Data.FollowedByMe != wantFollowed {
		t.Fatalf("%s: expected count=%d followed=%v, got count=%d followed=%v",
			ctx, wantCount, wantFollowed, resp.Data.FollowerCount, resp.Data.FollowedByMe)
	}
}
