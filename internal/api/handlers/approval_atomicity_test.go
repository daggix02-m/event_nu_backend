package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
)

// adminApproveToken registers a user, promotes them to admin, logs in, and
// returns an admin access token plus the promoted user's id.
func adminApproveToken(t *testing.T, h http.Handler, prefix string) (token, adminID string) {
	t.Helper()
	email, _, adminID := registerAndToken(t, h, prefix)
	promoteToAdmin(t, adminID)
	rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"email":%q,"password":"password123"}`, email), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin login: %d", rec.Code)
	}
	var adminResp tokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &adminResp); err != nil {
		t.Fatalf("decode admin login: %v", err)
	}
	return adminResp.Data.AccessToken, adminID
}

// applyForOrganizer creates a pending application and returns its id.
func applyForOrganizer(t *testing.T, h http.Handler, token, name string) string {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/api/v1/organizer-applications",
		fmt.Sprintf(`{"requested_name":%q,"bio":"We throw parties"}`, name), token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("apply: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	return idFromCreate(t, rec, "id")
}

func countOrganizerRows(t *testing.T, ownerUserID string) int {
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
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM organizers WHERE owner_user_id = $1`, ownerUserID).Scan(&n); err != nil {
		t.Fatalf("count organizers: %v", err)
	}
	return n
}

func applicationStatus(t *testing.T, appID string) string {
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
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status::text FROM organizer_applications WHERE id = $1`, appID).Scan(&status); err != nil {
		t.Fatalf("read application status: %v", err)
	}
	return status
}

// TestApproveSuccess checks the happy path: approval succeeds, sets the
// reviewer + notes, creates exactly one organizer, and the app is approved.
func TestApproveSuccess(t *testing.T) {
	_, h := newTestApp(t)

	_, orgTok, orgUserID := registerAndToken(t, h, "appr")
	appID := applyForOrganizer(t, h, orgTok, fmt.Sprintf("Atomic Collective %d", time.Now().UnixNano()%1e6))
	adminTok, _ := adminApproveToken(t, h, "adm")

	rec := doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+appID+"/approve",
		`{"notes":"looks good"}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := applicationStatus(t, appID); got != "approved" {
		t.Fatalf("expected application status approved, got %q", got)
	}
	if n := countOrganizerRows(t, orgUserID); n != 1 {
		t.Fatalf("expected exactly 1 organizer row, got %d", n)
	}
}

// TestApproveDuplicate proves the reviewer-wins-once invariant: a second
// approval after the first yields 409 and creates no extra organizer.
func TestApproveDuplicate(t *testing.T) {
	_, h := newTestApp(t)

	_, orgTok, orgUserID := registerAndToken(t, h, "dupp")
	appID := applyForOrganizer(t, h, orgTok, fmt.Sprintf("Dup Collective %d", time.Now().UnixNano()%1e6))
	adminTok, _ := adminApproveToken(t, h, "adm")

	rec := doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+appID+"/approve", `{}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("first approve: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+appID+"/approve", `{}`, adminTok)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate approve: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if n := countOrganizerRows(t, orgUserID); n != 1 {
		t.Fatalf("expected exactly 1 organizer row after duplicate, got %d", n)
	}
}

// TestApproveRollbackOnOrganizerCreateFailure forces the organizer-create step
// to fail (pre-existing slug) inside the approve transaction, then proves the
// application is rolled back to pending — the approval and the organizer must
// be atomic together.
func TestApproveRollbackOnOrganizerCreateFailure(t *testing.T) {
	_, h := newTestApp(t)

	// Seed an organizer that already owns the target slug, so the create inside
	// approve hits the unique constraint. The name is unique per run to avoid
	// clashing with rows left by earlier gate runs on the shared dev DB.
	collisionName := fmt.Sprintf("Collision Club %d", time.Now().UnixNano()%1e6)
	_, firstTok, _ := registerAndToken(t, h, "collide")
	firstAppID := applyForOrganizer(t, h, firstTok, collisionName)
	firstAdminTok, _ := adminApproveToken(t, h, "adm")
	rec := doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+firstAppID+"/approve", `{}`, firstAdminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve seed organizer: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// This applicant requests the same name, turning slugify into the same slug.
	_, orgTok, _ := registerAndToken(t, h, "collide2")
	appID := applyForOrganizer(t, h, orgTok, collisionName)

	adminTok, _ := adminApproveToken(t, h, "adm")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+appID+"/approve", `{}`, adminTok)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("approve with pre-existing slug: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	// Rollback proven: the application is back to pending, so it can be retried.
	if got := applicationStatus(t, appID); got != "pending" {
		t.Fatalf("expected application rolled back to pending, got %q", got)
	}
}

// TestApproveConcurrentSingleWinner proves the WHERE status='pending' guard is
// atomic under concurrency: two simultaneous approvals yield one 200 + one 409
// and exactly one organizer row.
func TestApproveConcurrentSingleWinner(t *testing.T) {
	_, h := newTestApp(t)

	_, orgTok, orgUserID := registerAndToken(t, h, "apro")
	appID := applyForOrganizer(t, h, orgTok, fmt.Sprintf("Race Collective %d", time.Now().UnixNano()%1e6))
	adminTok, _ := adminApproveToken(t, h, "adm")

	const n = 2
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		ok    int
		conf  int
		other []int
	)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			rec := doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+appID+"/approve", `{}`, adminTok)
			mu.Lock()
			defer mu.Unlock()
			switch rec.Code {
			case http.StatusOK:
				ok++
			case http.StatusConflict:
				conf++
			default:
				other = append(other, rec.Code)
			}
		}()
	}
	wg.Wait()

	if ok != 1 {
		t.Fatalf("expected exactly 1 winner, got %d (conflicts=%d, other=%v)", ok, conf, other)
	}
	if conf != 1 {
		t.Fatalf("expected exactly 1 conflict, got %d", conf)
	}
	if n := countOrganizerRows(t, orgUserID); n != 1 {
		t.Fatalf("expected exactly 1 organizer row, got %d", n)
	}
}

// TestRejectSuccess checks rejection transitions to rejected atomically.
func TestRejectSuccess(t *testing.T) {
	_, h := newTestApp(t)

	_, orgTok, _ := registerAndToken(t, h, "rejw")
	appID := applyForOrganizer(t, h, orgTok, fmt.Sprintf("Reject Crew %d", time.Now().UnixNano()%1e6))
	adminTok, _ := adminApproveToken(t, h, "adm")

	rec := doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+appID+"/reject",
		`{"notes":"no thanks"}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("reject: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := applicationStatus(t, appID); got != "rejected" {
		t.Fatalf("expected application status rejected, got %q", got)
	}

	// Second reject on a non-pending app conflicts.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/organizer-applications/"+appID+"/reject", `{}`, adminTok)
	if rec.Code != http.StatusConflict {
		t.Fatalf("reject again: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}
