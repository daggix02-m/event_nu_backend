package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type adminUserItem struct {
	ID         string    `json:"id"`
	Email      string    `json:"email"`
	Username   string    `json:"username"`
	Bio        string    `json:"bio"`
	PhotoURL   string    `json:"photo_url"`
	Role       string    `json:"role"`
	IsVerified bool      `json:"is_verified"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

type adminUsersResponse struct {
	Data []adminUserItem `json:"data"`
}

func getAdminUsers(t *testing.T, h http.Handler, q, token string) (*httptest.ResponseRecorder, adminUsersResponse) {
	t.Helper()
	rec := doJSON(t, h, http.MethodGet, "/api/v1/admin/users"+q, "", token)
	var resp adminUsersResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode admin users: %v; body=%s", err, rec.Body.String())
		}
	}
	return rec, resp
}

// TestAdminUserDirectory covers the console user list: search, role/status
// filters, invalid-filter rejection and pagination.
func TestAdminUserDirectory(t *testing.T) {
	_, h := newTestApp(t)
	adminTok, _ := adminApproveToken(t, h, "uadm")

	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	email, _, userID := registerAndToken(t, h, "dir"+unique)

	// Boundary: anonymous 401, non-admin 403.
	rec := doJSON(t, h, http.MethodGet, "/api/v1/admin/users", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list: expected 401, got %d", rec.Code)
	}
	_, plainTok, _ := registerAndToken(t, h, "uplain")
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/users", "", plainTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin list: expected 403, got %d", rec.Code)
	}

	// Search by email finds exactly the registered user.
	rec, found := getAdminUsers(t, h, "?q="+url.QueryEscape(email)+"&limit=100", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("directory search: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(found.Data) != 1 || found.Data[0].ID != userID {
		t.Fatalf("q=%s must find exactly 1 user with id %s, got %d", email, userID, len(found.Data))
	}
	if found.Data[0].Role != "user" || found.Data[0].Status != "active" {
		t.Fatalf("fresh user must be role=user status=active, got %+v", found.Data[0])
	}

	// role + status filters must return non-empty and consistent.
	rec, users := getAdminUsers(t, h, "?role=user&status=active&limit=100", adminTok)
	if rec.Code != http.StatusOK || len(users.Data) == 0 {
		t.Fatalf("role/status filter: expected 200 with rows, got %d", rec.Code)
	}
	for _, u := range users.Data {
		if u.Role != "user" || u.Status != "active" {
			t.Fatalf("filter mismatch: %+v", u)
		}
	}

	// Invalid filters rejected.
	for _, q := range []string{"?role=nope", "?status=nope"} {
		rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/users"+q, "", adminTok)
		if rec.Code == http.StatusOK {
			t.Fatalf("invalid filter %q must not be accepted, got 200", q)
		}
	}

	// Pagination contract on the whole directory (non-empty shared DB).
	rec, paged := getAdminUsers(t, h, "?limit=1&page=1", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("paged directory: expected 200, got %d", rec.Code)
	}
	var meta struct {
		Pagination struct {
			Page    int  `json:"page"`
			Limit   int  `json:"limit"`
			Total   int  `json:"total"`
			HasNext bool `json:"has_next"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("decode pagination: %v", err)
	}
	if len(paged.Data) != 1 || meta.Pagination.Total < 1 {
		t.Fatalf("limit=1 must return 1 row with total>=1, got %+v", meta.Pagination)
	}
}

// TestAdminUserUpdateAndGuards covers profile edits, validation and the
// self-demote/self-suspend guard.
func TestAdminUserUpdateAndGuards(t *testing.T) {
	_, h := newTestApp(t)
	adminTok, adminID := adminApproveToken(t, h, "uupd")

	_, _, targetID := registerAndToken(t, h, "uupdtarget")
	unique := fmt.Sprintf("%d", time.Now().UnixNano())

	// Rename the target.
	rec := doJSON(t, h, http.MethodPatch, "/api/v1/admin/users/"+targetID,
		fmt.Sprintf(`{"username":%q,"bio":"edited by admin"}`, "editedbyadmin"+unique), adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var renamed struct {
		Data adminUserItem `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &renamed); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	expectUsername := "editedbyadmin" + unique
	if renamed.Data.Username != expectUsername {
		t.Fatalf("rename took effect incorrectly: got %q want %q", renamed.Data.Username, expectUsername)
	}
	if renamed.Data.Bio != "edited by admin" {
		t.Fatalf("bio edit took effect incorrectly: got %q", renamed.Data.Bio)
	}
	if !strings.Contains(rec.Body.String(), `"role":"user"`) {
		t.Fatalf("update must not change role when not requested: %s", rec.Body.String())
	}

	// Promote to admin, then guard the action: update the target's role via the
	// directory and confirm the role change took effect.
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/admin/users/"+targetID,
		`{"role":"admin"}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("promote via directory: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Degenerate bodies → 422.
	for _, body := range []string{`{}`, `{"username":""}`, `{"role":"nope"}`, `{"status":"nope"}`} {
		rec = doJSON(t, h, http.MethodPatch, "/api/v1/admin/users/"+targetID, body, adminTok)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("body %s: expected 422, got %d: %s", body, rec.Code, rec.Body.String())
		}
	}

	// Self role/status changes blocked.
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/admin/users/"+adminID, `{"role":"user"}`, adminTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("self-demote: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/admin/users/"+adminID, `{"status":"suspended"}`, adminTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("self-suspend: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// Well-formed but nonexistent target → 404.
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/admin/users/9f5f0a2c-0000-0000-0000-000000000001", `{"bio":"x"}`, adminTok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing target: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAdminUserSuspendRestore covers suspend (login blocked), restore (login
// allowed again) and demote-kills-admin via the existing RequireAdmin recheck.
func TestAdminUserSuspendRestore(t *testing.T) {
	_, h := newTestApp(t)
	adminTok, adminID := adminApproveToken(t, h, "usus")

	// A second admin will be demoted at the end.
	secondEmail, _, secondAdminID := registerAndToken(t, h, "usus2")
	promoteToAdmin(t, secondAdminID)
	secondTok := loginToken(t, h, secondEmail)

	// Target a normal user.
	email, _, targetID := registerAndToken(t, h, "usustarget")

	// Suspend → login blocked.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/admin/users/"+targetID+"/suspend", "", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("suspend: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"email":%q,"password":"password123"}`, email), "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("suspended login: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	// Suspend is idempotent.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/users/"+targetID+"/suspend", "", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("idempotent suspend: expected 200, got %d", rec.Code)
	}

	// Restore → login works again.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/users/"+targetID+"/restore", "", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"email":%q,"password":"password123"}`, email), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("restored login: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Self-suspend blocked.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/users/"+adminID+"/suspend", "", adminTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("self-suspend: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// Missing target → 404.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/users/9f5f0a2c-0000-0000-0000-000000000001/suspend", "", adminTok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("suspend missing user: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	// Demote-kills-admin: the demoted admin loses access on the very next
	// request (RequireAdmin re-reads role from the DB each time).
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/admin/users/"+secondAdminID, `{"role":"user"}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("demote second admin: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/users", "", secondTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("demoted admin must lose access: expected 403, got %d", rec.Code)
	}
}
