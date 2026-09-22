package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

type applicationItem struct {
	ID                string     `json:"id"`
	UserID            string     `json:"user_id"`
	RequestedName     string     `json:"requested_name"`
	Status            string     `json:"status"`
	ApplicantEmail    *string    `json:"applicant_email"`
	ApplicantUsername *string    `json:"applicant_username"`
	ReviewedBy        *string    `json:"reviewed_by"`
	ReviewedAt        *time.Time `json:"reviewed_at"`
}

type applicationsResponse struct {
	Data       []applicationItem `json:"data"`
	Pagination struct {
		Page    int  `json:"page"`
		Limit   int  `json:"limit"`
		Total   int  `json:"total"`
		HasNext bool `json:"has_next"`
	} `json:"pagination"`
}

func getApplications(t *testing.T, h http.Handler, q, token string) applicationsResponse {
	t.Helper()
	rec := doJSON(t, h, http.MethodGet, "/api/v1/admin/organizer-applications"+q, "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("list applications: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp applicationsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode applications: %v; body=%s", err, rec.Body.String())
	}
	return resp
}

// TestAdminListApplications covers the moderation queue: pending-first ordering,
// applicant identity, status filtering, pagination, and the admin gate.
func TestAdminListApplications(t *testing.T) {
	_, h := newTestApp(t)

	// Two applicants apply in succession so created_at ordering is observable.
	_, orgTokA, _ := registerAndToken(t, h, "appa")
	appNameA := fmt.Sprintf("Alpha Crew %d", time.Now().UnixNano())
	rec := doJSON(t, h, http.MethodPost, "/api/v1/organizer-applications",
		fmt.Sprintf(`{"requested_name":%q,"bio":"first"}`, appNameA), orgTokA)
	if rec.Code != http.StatusCreated {
		t.Fatalf("apply A: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	appA := idFromCreate(t, rec, "id")

	time.Sleep(20 * time.Millisecond)

	emailB, orgTokB, _ := registerAndToken(t, h, "appb")
	appNameB := fmt.Sprintf("Beta Crew %d", time.Now().UnixNano())
	rec = doJSON(t, h, http.MethodPost, "/api/v1/organizer-applications",
		fmt.Sprintf(`{"requested_name":%q,"bio":"second"}`, appNameB), orgTokB)
	if rec.Code != http.StatusCreated {
		t.Fatalf("apply B: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	appB := idFromCreate(t, rec, "id")

	// Authentication boundary: anonymous is 401, non-admin is 403.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/organizer-applications", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list: expected 401, got %d", rec.Code)
	}
	_, plainTok, _ := registerAndToken(t, h, "plain")
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/organizer-applications", "", plainTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin list: expected 403, got %d", rec.Code)
	}

	// Admin views the queue.
	adminEmail, _, adminID := registerAndToken(t, h, "admin")
	promoteToAdmin(t, adminID)
	adminTok := loginToken(t, h, adminEmail)

	// Both applications are visible with applicant identity attached.
	resp := getApplications(t, h, "?limit=100", adminTok)
	byID := make(map[string]applicationItem, len(resp.Data))
	for _, it := range resp.Data {
		byID[it.ID] = it
	}
	a, okA := byID[appA]
	b, okB := byID[appB]
	if !okA || !okB {
		t.Fatalf("queue must contain both applications (found %d items)", len(resp.Data))
	}
	if a.ApplicantEmail == nil || a.ApplicantUsername == nil || *a.ApplicantUsername == "" {
		t.Fatalf("application A must carry applicant email+username, got email=%v", a.ApplicantEmail)
	}
	if b.ApplicantEmail == nil || *b.ApplicantEmail != emailB {
		t.Fatalf("application B must carry the applicant email %q, got %v", emailB, b.ApplicantEmail)
	}

	// Approve B (the more recent) so A stays pending; the default queue must
	// then order pending A before approved B.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+appB+"/approve",
		`{"notes":"ok"}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve B: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp = getApplications(t, h, "?limit=100", adminTok)
	byID = make(map[string]applicationItem, len(resp.Data))
	for _, it := range resp.Data {
		byID[it.ID] = it
	}
	var posA, posB = -1, -1
	for i, it := range resp.Data {
		switch it.ID {
		case appA:
			posA = i
		case appB:
			posB = i
		}
	}
	if posA == -1 || posB == -1 {
		t.Fatal("queue must still contain both applications after approval")
	}
	if posA >= posB {
		t.Fatalf("pending application A (index %d) must sort before approved B (index %d)", posA, posB)
	}
	if b := byID[appB]; b.ReviewedBy == nil {
		t.Fatal("approved application must record the reviewer")
	}

	// Status filter narrows to a single state (scoped to this test's rows —
	// the shared test DB accumulates applications across prior runs).
	pending := getApplications(t, h, "?status=pending&limit=100", adminTok)
	if !hasApp(pending.Data, appA) || hasApp(pending.Data, appB) {
		t.Fatalf("status=pending must include A and exclude B, got %d items", len(pending.Data))
	}
	approved := getApplications(t, h, "?status=approved&limit=100", adminTok)
	if !hasApp(approved.Data, appB) || hasApp(approved.Data, appA) {
		t.Fatalf("status=approved must include B and exclude A, got %d items", len(approved.Data))
	}
	withdrawn := getApplications(t, h, "?status=withdrawn", adminTok)
	if hasApp(withdrawn.Data, appA) || hasApp(withdrawn.Data, appB) {
		t.Fatalf("neither application may appear in status=withdrawn, got %d items", len(withdrawn.Data))
	}

	// An unknown status value is rejected, never silently ignored.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/organizer-applications?status=nope", "", adminTok)
	if rec.Code == http.StatusOK {
		t.Fatalf("unknown status must not be accepted, got 200")
	}

	// Pagination contract: page carries max `limit` items and a total.
	paged := getApplications(t, h, "?limit=1&page=1", adminTok)
	if len(paged.Data) != 1 || !paged.Pagination.HasNext || paged.Pagination.Total < 2 {
		t.Fatalf("limit=1 must return 1 item with has_next and total, got %+v", paged.Pagination)
	}
}

func hasApp(items []applicationItem, id string) bool {
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}

func loginToken(t *testing.T, h http.Handler, email string) string {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"email":%q,"password":"password123"}`, email), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin login: %d: %s", rec.Code, rec.Body.String())
	}
	var resp tokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	return resp.Data.AccessToken
}
