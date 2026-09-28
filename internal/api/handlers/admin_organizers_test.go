package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
)

type adminOrganizerItem struct {
	ID          string  `json:"id"`
	OwnerUserID *string `json:"owner_user_id"`
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Bio         string  `json:"bio"`
	Status      string  `json:"status"`
	LogoURL     string  `json:"logo_url"`
	EventCount  int     `json:"event_count"`
}

type adminOrganizerListResponse struct {
	Data []adminOrganizerItem `json:"data"`
}

type adminOrganizerDetailResponse struct {
	Data struct {
		adminOrganizerItem
		RecentEvents []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"recent_events"`
	} `json:"data"`
}

func getAdminOrganizerList(t *testing.T, h http.Handler, q, token string) (*httptest.ResponseRecorder, adminOrganizerListResponse) {
	t.Helper()
	rec := doJSON(t, h, http.MethodGet, "/api/v1/admin/organizers"+q, "", token)
	var resp adminOrganizerListResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode admin organizers: %v; body=%s", err, rec.Body.String())
		}
	}
	return rec, resp
}

// TestAdminOrganizerCreateAndDetail covers direct (non-apply) provisioning: a
// standalone profile, an owner-linked one (single-organizer invariant), slug
// conflicts, validation, and the detail response shape.
func TestAdminOrganizerCreateAndDetail(t *testing.T) {
	_, h := newTestApp(t)
	adminTok, _ := adminApproveToken(t, h, "orgadm")

	unique := time.Now().UnixNano()

	// Boundary: anonymous 401, non-admin 403.
	rec := doJSON(t, h, http.MethodGet, "/api/v1/admin/organizers", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list: expected 401, got %d", rec.Code)
	}
	_, plainTok, _ := registerAndToken(t, h, "orgplain")
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/organizers", "", plainTok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin list: expected 403, got %d", rec.Code)
	}

	// Well-formed but nonexistent id → clean 404 (sentinel mapping).
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/organizers/9f5f0a2c-0000-0000-0000-000000000001", "", adminTok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing organizer: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	// Standalone create (no owner_user_id): active immediately, slug derived.
	name := fmt.Sprintf("Stage One %d", unique)
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizers",
		fmt.Sprintf(`{"name":%q,"bio":"house parties"}`, name), adminTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create standalone: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	orgID := idFromCreate(t, rec, "id")

	// Detail: profile + null owner + zero events.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/organizers/"+orgID, "", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var detail adminOrganizerDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Data.Name != name || detail.Data.Status != "active" {
		t.Fatalf("detail mismatch: %+v", detail.Data)
	}
	if detail.Data.Slug != fmt.Sprintf("stage-one-%d", unique) {
		t.Fatalf("slug derivation mismatch: %q", detail.Data.Slug)
	}
	if detail.Data.OwnerUserID != nil {
		t.Fatalf("standalone profile must have null owner_user_id, got %v", *detail.Data.OwnerUserID)
	}
	if detail.Data.EventCount != 0 || len(detail.Data.RecentEvents) != 0 {
		t.Fatalf("fresh organizer must have 0 events, got count=%d recent=%d", detail.Data.EventCount, len(detail.Data.RecentEvents))
	}

	// Slug conflict → 409 (same name slugifies identically).
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizers",
		fmt.Sprintf(`{"name":%q,"bio":"dup"}`, name), adminTok)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate slug: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// Owner-linked create, then enforce the single-organizer invariant.
	_, _, userID := registerAndToken(t, h, "orgowner")
	ownerName := fmt.Sprintf("Owner Lab %d", unique)
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizers",
		fmt.Sprintf(`{"name":%q,"bio":"x","owner_user_id":%q}`, ownerName, userID), adminTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create owner-linked: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	ownerOrgID := idFromCreate(t, rec, "id")
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/organizers/"+ownerOrgID, "", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner-linked detail: expected 200, got %d", rec.Code)
	}
	var ownerDetail adminOrganizerDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ownerDetail); err != nil {
		t.Fatalf("decode owner detail: %v", err)
	}
	if ownerDetail.Data.OwnerUserID == nil || *ownerDetail.Data.OwnerUserID != userID {
		t.Fatalf("owner-linked profile must carry owner_user_id=%s, got %+v", userID, ownerDetail.Data.OwnerUserID)
	}

	// Second organizer for the same user → 409.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizers",
		fmt.Sprintf(`{"name":%q,"bio":"y","owner_user_id":%q}`, "Owner Lab Two "+fmt.Sprint(unique), userID), adminTok)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second organizer for same owner: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// Unknown linked owner → 422.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizers",
		fmt.Sprintf(`{"name":%q,"bio":"x","owner_user_id":%q}`, "Ghost Owner "+fmt.Sprint(unique), "00000000-0000-0000-0000-000000000000"), adminTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown owner: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// Validation: short name and bogus status.
	for _, body := range []string{
		`{"name":"ab"}`,
		fmt.Sprintf(`{"name":%q,"status":"nope"}`, "Invalid Status "+fmt.Sprint(unique)),
	} {
		rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizers", body, adminTok)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("validation: expected 422, got %d for %s", rec.Code, body)
		}
	}
}

// TestAdminOrganizerList covers search, status filtering and pagination on the
// console organizer list.
func TestAdminOrganizerList(t *testing.T) {
	_, h := newTestApp(t)
	adminTok, _ := adminApproveToken(t, h, "orglst")

	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	keyword := "Orchid" + unique
	for i := 0; i < 3; i++ {
		rec := doJSON(t, h, http.MethodPost, "/api/v1/admin/organizers",
			fmt.Sprintf(`{"name":%q,"bio":"noise"}`, fmt.Sprintf("%s %d", keyword, i)), adminTok)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %d: expected 201, got %d: %s", i, rec.Code, rec.Body.String())
		}
	}

	// Search must find all three by name substring.
	rec, found := getAdminOrganizerList(t, h, "?q="+keyword+"&limit=100", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("search list: expected 200, got %d", rec.Code)
	}
	if len(found.Data) != 3 {
		t.Fatalf("search %q must return exactly 3, got %d", keyword, len(found.Data))
	}
	for _, item := range found.Data {
		if item.Status != "active" {
			t.Fatalf("admin-created organizer must be active, got %q", item.Status)
		}
	}

	// Unknown status filter is rejected.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/organizers?status=nope", "", adminTok)
	if rec.Code == http.StatusOK {
		t.Fatalf("invalid status filter must not be accepted, got 200")
	}

	// Pagination contract.
	pageTok := "p" + keyword
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizers",
		fmt.Sprintf(`{"name":%q,"bio":"page"}`, pageTok), adminTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create page org: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	rec, paged := getAdminOrganizerList(t, h, "?q="+pageTok+"&limit=1&page=1", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("paged list: expected 200, got %d", rec.Code)
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
	if len(paged.Data) != 1 || meta.Pagination.Total != 1 || meta.Pagination.HasNext {
		t.Fatalf("limit=1 for one match must return 1 item, total 1, no next; got %+v", meta.Pagination)
	}
}

// TestAdminOrganizerListSeesSuspendedOrganizers ensures the admin list (unlike
// the public reads) still surfaces rows that are not currently active.
func TestAdminOrganizerListSeesSuspendedOrganizers(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	_, h := newTestApp(t)
	adminTok, adminID := adminApproveToken(t, h, "orgsus")

	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	rec := doJSON(t, h, http.MethodPost, "/api/v1/admin/organizers",
		fmt.Sprintf(`{"name":%q,"bio":"x","status":"suspended","owner_user_id":%q}`, fmt.Sprintf("Susp %s", unique), adminID), adminTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create suspended: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	orgID := idFromCreate(t, rec, "id")

	if _, err := suspendOrganizer(t, orgID); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	rec, listed := getAdminOrganizerList(t, h, "?status=suspended&q="+unique, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin suspended list: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(listed.Data) == 0 {
		t.Fatalf("suspended organizer must stay visible to admins")
	}
}

// suspendOrganizer flips an organizer to suspended using the trusted service
// role (the state the DB would be in after a suspension action).
func suspendOrganizer(t *testing.T, orgID string) (int, error) {
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
	tag, err := pool.Exec(context.Background(), `UPDATE organizers SET status = 'suspended' WHERE id = $1`, orgID)
	return int(tag.RowsAffected()), err
}
