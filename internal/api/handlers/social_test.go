package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestCommentLifecycle covers create/list/update/delete of event comments,
// including ownership enforcement and the published-only visibility gate.
func TestCommentLifecycle(t *testing.T) {
	_, h := newTestApp(t)

	orgTok, _ := promoteOrganizer(t, h, "cmtorg", fmt.Sprintf("Comment Org %d", time.Now().UnixNano()))
	eventID := createPublishedEvent(t, h, orgTok, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))

	_, commenterTok, _ := registerAndToken(t, h, "cmtu")
	_, otherTok, _ := registerAndToken(t, h, "cmto")

	// Create a comment as an authenticated user.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/comments", `{"body":"Can't wait!"}`, commenterTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct {
			ID     string `json:"id"`
			Body   string `json:"body"`
			UserID string `json:"user_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode comment: %v", err)
	}
	commentID := created.Data.ID
	if created.Data.Body != "Can't wait!" || created.Data.UserID == "" {
		t.Fatalf("unexpected comment payload: %+v", created.Data)
	}

	// Anonymous users may read the comment list (public route).
	resp := decodePaginated(t, doJSON(t, h, http.MethodGet, "/api/v1/events/"+eventID+"/comments", "", ""))
	if resp.Pagination.Total != 1 || len(resp.Data) != 1 || resp.Data[0]["id"] != commentID {
		t.Fatalf("expected exactly the created comment in the public list, got %+v", resp)
	}

	// Non-owner cannot update.
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/comments/"+commentID, `{"body":"hijacked"}`, otherTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("update by non-owner: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	// Owner updates successfully.
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/comments/"+commentID, `{"body":"Edited!"}`, commenterTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("update comment: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated struct {
		Data struct {
			Body string `json:"body"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated comment: %v", err)
	}
	if updated.Data.Body != "Edited!" {
		t.Fatalf("expected updated body, got %q", updated.Data.Body)
	}

	// Non-owner cannot delete.
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/comments/"+commentID, "", otherTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("delete by non-owner: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	// Owner deletes; the comment disappears from the public list.
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/comments/"+commentID, "", commenterTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete comment: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp = decodePaginated(t, doJSON(t, h, http.MethodGet, "/api/v1/events/"+eventID+"/comments", "", ""))
	if resp.Pagination.Total != 0 {
		t.Fatalf("expected no comments after delete, got %+v", resp)
	}

	// Unauthenticated create is rejected.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/comments", `{"body":"anon"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous comment: expected 401, got %d", rec.Code)
	}

	// Comments can only attach to published (publicly visible) events: a draft
	// event is invisible to the commenter even though they could create it.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events",
		fmt.Sprintf(`{"title":"Draft %d","starts_at":%q,"price_is_free":true}`, time.Now().UnixNano(), time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339)), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create draft: expected 201, got %d", rec.Code)
	}
	draftID := idFromCreate(t, rec, "id")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+draftID+"/comments", `{"body":"nope"}`, commenterTok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("comment on draft event: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestValidationErrorsCommentBody proves empty and oversized comment bodies are
// rejected with 422 before any write.
func TestValidationErrorsCommentBody(t *testing.T) {
	_, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "cmtvorg", fmt.Sprintf("Val Org %d", time.Now().UnixNano()))
	eventID := createPublishedEvent(t, h, orgTok, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
	_, userTok, _ := registerAndToken(t, h, "cmtvu")

	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/comments", `{"body":"   "}`, userTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("blank body: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/comments", fmt.Sprintf(`{"body":%q}`, strings.Repeat("a", 2001)), userTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("oversized body: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestLikeToggle covers liking/unliking an event, idempotent unlike, the like
// state surfaced on event detail (per-request user), and anonymous reads.
func TestLikeToggle(t *testing.T) {
	_, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "likeorg", fmt.Sprintf("Like Org %d", time.Now().UnixNano()))
	eventID := createPublishedEvent(t, h, orgTok, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
	_, userA, _ := registerAndToken(t, h, "likea")
	_, userB, _ := registerAndToken(t, h, "likeb")

	// Like as user A.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/like", "", userA)
	if rec.Code != http.StatusOK {
		t.Fatalf("like: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var like struct {
		Data struct {
			Liked     bool `json:"liked"`
			LikeCount int  `json:"like_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &like); err != nil {
		t.Fatalf("decode like response: %v", err)
	}
	if !like.Data.Liked || like.Data.LikeCount != 1 {
		t.Fatalf("expected liked=true count=1, got %+v", like.Data)
	}

	// A duplicate like is a no-op (idempotent).
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/like", "", userA)
	if rec.Code != http.StatusOK || reqLikeCount(t, rec) != 1 {
		t.Fatalf("duplicate like: expected 200 count=1, got %d %s", rec.Code, rec.Body.String())
	}

	// Second user likes: count is 2.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/like", "", userB)
	if rec.Code != http.StatusOK || reqLikeCount(t, rec) != 2 {
		t.Fatalf("second like: expected count=2, got %d %s", rec.Code, rec.Body.String())
	}

	// Event detail reflects per-user state: A sees liked_by_me=true, count=2;
	// an anonymous reader sees count=2 but liked_by_me=false.
	assertEventLikeState(t, h, eventID, userA, true, 2)
	assertEventLikeState(t, h, eventID, "", false, 2)

	// Unlike is a 200 with liked=false; unliking again still succeeds.
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/events/"+eventID+"/like", "", userB)
	if rec.Code != http.StatusOK || reqLikeCount(t, rec) != 1 {
		t.Fatalf("unlike: expected count=1, got %d %s", rec.Code, rec.Body.String())
	}
	assertEventLikeState(t, h, eventID, "", false, 1)
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/events/"+eventID+"/like", "", userB)
	if rec.Code != http.StatusOK || reqLikeCount(t, rec) != 1 {
		t.Fatalf("idempotent unlike: expected 200 count=1, got %d %s", rec.Code, rec.Body.String())
	}

	// Liking a draft event is refused (it is not publicly visible).
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events",
		fmt.Sprintf(`{"title":"Draft %d","starts_at":%q,"price_is_free":true}`, time.Now().UnixNano(), time.Now().Add(72*time.Hour).UTC().Format(time.RFC3339)), orgTok)
	draftID := idFromCreate(t, rec, "id")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+draftID+"/like", "", userA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("like draft event: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	// Anonymous users cannot like at all.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/like", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous like: expected 401, got %d", rec.Code)
	}
}

func reqLikeCount(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			LikeCount int `json:"like_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode like response: %v", err)
	}
	return resp.Data.LikeCount
}

func assertEventLikeState(t *testing.T, h http.Handler, eventID, token string, wantLiked bool, wantCount int) {
	t.Helper()
	var rec *httptest.ResponseRecorder
	if token == "" {
		rec = doJSON(t, h, http.MethodGet, "/api/v1/events/"+eventID, "", "")
	} else {
		rec = doJSON(t, h, http.MethodGet, "/api/v1/events/"+eventID, "", token)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("event detail: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			LikeCount int  `json:"like_count"`
			LikedByMe bool `json:"liked_by_me"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode event detail: %v", err)
	}
	if resp.Data.LikeCount != wantCount || resp.Data.LikedByMe != wantLiked {
		t.Fatalf("expected like_count=%d liked_by_me=%v, got count=%d liked=%v", wantCount, wantLiked, resp.Data.LikeCount, resp.Data.LikedByMe)
	}
}

// TestUpdateProfile covers PATCH /users/me: partial update, the empty-body 422,
// duplicate-username 409, and that changes persist across reads.
func TestUpdateProfile(t *testing.T) {
	_, h := newTestApp(t)

	email := fmt.Sprintf("prof+%d@test.example", time.Now().UnixNano())
	reg := registerUser(t, h, email)

	// Empty body changes nothing and is rejected.
	rec := doJSON(t, h, http.MethodPatch, "/api/v1/users/me", `{}`, reg.Data.AccessToken)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty PATCH: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// Partial update: only bio changes.
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/users/me", `{"bio":"I love events"}`, reg.Data.AccessToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH bio: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated struct {
		Data struct {
			Username string `json:"username"`
			Bio      string `json:"bio"`
			PhotoURL string `json:"photo_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if updated.Data.Username != reg.Data.User.Username {
		t.Fatalf("username must be unchanged, got %q want %q", updated.Data.Username, reg.Data.User.Username)
	}
	if updated.Data.Bio != "I love events" {
		t.Fatalf("expected bio updated, got %q", updated.Data.Bio)
	}

	// Invalid username is rejected.
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/users/me", `{"username":"ab"}`, reg.Data.AccessToken)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("short username: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// Persisted across a fresh read.
	var me struct {
		Data struct {
			Bio string `json:"bio"`
		} `json:"data"`
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/users/me", "", reg.Data.AccessToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET me: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.Data.Bio != "I love events" {
		t.Fatalf("expected persisted bio, got %q", me.Data.Bio)
	}

	// Taking another user's username conflicts.
	_, otherTok, _ := registerAndToken(t, h, "prof2")
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/users/me", fmt.Sprintf(`{"username":%q}`, updated.Data.Username), otherTok)
	if rec.Code != http.StatusConflict {
		t.Fatalf("taken username: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}
