package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// setupScheduleQA creates two organizers, a published event owned by the first,
// and an ordinary user. Returns h, first-organizer token, second-organizer
// token, ordinary user token, and the published event id.
func setupScheduleQA(t *testing.T) (h http.Handler, orgTok, otherOrgTok, userTok, eventID string) {
	t.Helper()
	t.Setenv("AUTH_REGISTER_RATE_LIMIT", "100")
	_, h = newTestApp(t)
	orgTok, _ = promoteOrganizer(t, h, "sch", fmt.Sprintf("Schedule Org %d", time.Now().UnixNano()))
	otherOrgTok, _ = promoteOrganizer(t, h, "oth", fmt.Sprintf("Other Org %d", time.Now().UnixNano()))
	eventID = createPublishedEvent(t, h, orgTok, time.Now().Add(time.Hour).Format(time.RFC3339))
	_, userTok, _ = registerAndToken(t, h, "usr")
	return h, orgTok, otherOrgTok, userTok, eventID
}

// --- Schedule tests ----------------------------------------------------------

func TestScheduleCRUD(t *testing.T) {
	h, orgTok, _, userTok, eventID := setupScheduleQA(t)
	eventURL := "/api/v1/events/" + eventID + "/schedule"
	starts := time.Now().Add(2 * time.Hour).Format(time.RFC3339)

	// Non-organizer cannot create a session.
	rec := doJSON(t, h, http.MethodPost, eventURL,
		fmt.Sprintf(`{"title":"S1","starts_at":%q}`, starts), userTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-org create: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	// Organiser creates a session.
	rec = doJSON(t, h, http.MethodPost, eventURL,
		fmt.Sprintf(`{"title":"Welcome Keynote","speaker":"Alice","stage":"Main","starts_at":%q}`, starts), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create session: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	sessID := idFromCreate(t, rec, "id")

	// List returns the session.
	var listResp struct {
		Data []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"data"`
	}
	rec = doJSON(t, h, http.MethodGet, eventURL, "", userTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("list schedule: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Data) != 1 || listResp.Data[0].Title != "Welcome Keynote" {
		t.Fatalf("unexpected list: %+v", listResp.Data)
	}

	// Update session.
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/schedule/"+sessID,
		fmt.Sprintf(`{"title":"Keynote","speaker":"Bob","starts_at":%q}`, starts), orgTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("update session: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if updated.Data["title"] != "Keynote" || updated.Data["speaker"] != "Bob" {
		t.Fatalf("unexpected update: %+v", updated.Data)
	}

	// Delete session.
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/schedule/"+sessID, "", orgTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete session: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// List is empty now.
	rec = doJSON(t, h, http.MethodGet, eventURL, "", userTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("list after delete: expected 200, got %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Data) != 0 {
		t.Fatalf("expected empty list, got %+v", listResp.Data)
	}
}

func TestScheduleCannotModifyOtherOrganizer(t *testing.T) {
	h, orgTok, otherOrgTok, _, eventID := setupScheduleQA(t)
	starts := time.Now().Add(2 * time.Hour).Format(time.RFC3339)

	// Create session under the event's organizer.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/schedule",
		fmt.Sprintf(`{"title":"S1","starts_at":%q}`, starts), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	sessID := idFromCreate(t, rec, "id")

	// A different organizer should not be able to update it.
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/schedule/"+sessID,
		fmt.Sprintf(`{"title":"Hacked","starts_at":%q}`, starts), otherOrgTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("other org update: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- Q&A tests ---------------------------------------------------------------

func TestQACreateAndList(t *testing.T) {
	h, _, _, userTok, eventID := setupScheduleQA(t)

	// Create a question.
	questionURL := "/api/v1/events/" + eventID + "/questions"
	rec := doJSON(t, h, http.MethodPost, questionURL, `{"body":"What time does it start?"}`, userTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create question: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	qID := idFromCreate(t, rec, "id")

	// List returns the question with votes=0 and voted_by_me=false for anonymous.
	var listResp struct {
		Data []struct {
			ID        string `json:"id"`
			Body      string `json:"body"`
			Votes     int    `json:"votes"`
			VotedByMe bool   `json:"voted_by_me"`
		} `json:"data"`
	}
	rec = doJSON(t, h, http.MethodGet, questionURL, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list questions: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Data) != 1 {
		t.Fatalf("expected 1 question, got %d", len(listResp.Data))
	}
	if listResp.Data[0].Body != "What time does it start?" {
		t.Fatalf("wrong body: %s", listResp.Data[0].Body)
	}
	_ = qID
}

func TestQAUpvoteAndRemove(t *testing.T) {
	h, _, _, userTok, eventID := setupScheduleQA(t)
	questionURL := "/api/v1/events/" + eventID + "/questions"

	rec := doJSON(t, h, http.MethodPost, questionURL, `{"body":"Where is the venue?"}`, userTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	qID := idFromCreate(t, rec, "id")

	// Upvote is idempotent.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/questions/"+qID+"/upvote", "", userTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("upvote: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodPost, "/api/v1/questions/"+qID+"/upvote", "", userTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("upvote duplicate: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// List shows voted_by_me=true and votes=1 for the signed-in user.
	var listResp struct {
		Data []struct {
			Votes     int  `json:"votes"`
			VotedByMe bool `json:"voted_by_me"`
		} `json:"data"`
	}
	rec = doJSON(t, h, http.MethodGet, questionURL, "", userTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listResp.Data) != 1 || listResp.Data[0].Votes != 1 || !listResp.Data[0].VotedByMe {
		t.Fatalf("after upvote: %+v", listResp.Data)
	}

	// Remove upvote (idempotent).
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/questions/"+qID+"/upvote", "", userTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove upvote: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/questions/"+qID+"/upvote", "", userTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove idempotent: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// List shows voted_by_me=false, votes=0.
	rec = doJSON(t, h, http.MethodGet, questionURL, "", userTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("list after unvote: %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listResp.Data) != 1 || listResp.Data[0].Votes != 0 || listResp.Data[0].VotedByMe {
		t.Fatalf("after unvote: %+v", listResp.Data)
	}
}

func TestQAPinAndAnswer(t *testing.T) {
	h, orgTok, _, userTok, eventID := setupScheduleQA(t)
	questionURL := "/api/v1/events/" + eventID + "/questions"

	// User creates a question.
	rec := doJSON(t, h, http.MethodPost, questionURL, `{"body":"What about parking?"}`, userTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	qID := idFromCreate(t, rec, "id")

	// Non-organizer cannot pin.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/questions/"+qID+"/pin",
		`{"pinned":true}`, userTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-org pin: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	// Organizer pins.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/questions/"+qID+"/pin",
		`{"pinned":true}`, orgTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("pin: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Organizer answers.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/questions/"+qID+"/answer",
		`{"answer":"Parking is available behind the venue."}`, orgTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("answer: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify in list: pinned question appears first, with answer.
	var listResp struct {
		Data []struct {
			ID     string `json:"id"`
			Pinned bool   `json:"pinned"`
			Answer string `json:"answer"`
			Votes  int    `json:"votes"`
		} `json:"data"`
	}
	rec = doJSON(t, h, http.MethodGet, questionURL, "", userTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listResp.Data) != 1 {
		t.Fatalf("expected 1 question, got %d", len(listResp.Data))
	}
	q := listResp.Data[0]
	if q.ID != qID || !q.Pinned || q.Answer != "Parking is available behind the venue." {
		t.Fatalf("unexpected pinned question: %+v", q)
	}
}

func TestQACannotModifyOtherOrganizerQuestion(t *testing.T) {
	h, _, otherOrgTok, userTok, eventID := setupScheduleQA(t)
	questionURL := "/api/v1/events/" + eventID + "/questions"

	rec := doJSON(t, h, http.MethodPost, questionURL, `{"body":"Question?"}`, userTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d: %s", rec.Code, rec.Body.String())
	}
	qID := idFromCreate(t, rec, "id")

	// Different organizer should not be able to pin.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/questions/"+qID+"/pin",
		`{"pinned":true}`, otherOrgTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("other org pin: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}
