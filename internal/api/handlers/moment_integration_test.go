package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/repository"
	"github.com/daggix02-m/event_nu/backend/internal/worker"
)

type momentResp struct {
	Data struct {
		ID           string `json:"id"`
		EventID      string `json:"event_id"`
		UserID       string `json:"user_id"`
		MediaAssetID string `json:"media_asset_id"`
		Caption      string `json:"caption"`
	} `json:"data"`
}

type attendeeItem struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
}

// startMediaWorker runs the media consumer fast so uploaded assets reach
// 'ready' during the test.
func startMediaWorker(t *testing.T, app *api.Application) func() {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Fatal("DATABASE_URL required for moments integration test")
	}
	pool, err := database.NewPoolWithRole(context.Background(), dsn, "service")
	if err != nil {
		t.Fatalf("service pool: %v", err)
	}
	cfg := app.Config
	cfg.MediaJobPollInterval = 50 * time.Millisecond
	consumer := worker.NewMediaConsumer(testLogger(), cfg, repository.NewMediaRepository(pool), app.Storage)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = consumer.Run(ctx) }()
	return cancel
}

// waitMediaReady polls GET /media/{id} until status == ready.
func waitMediaReady(t *testing.T, h http.Handler, id string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		rec := doJSON(t, h, http.MethodGet, "/api/v1/media/"+id, "", "")
		if rec.Code == http.StatusOK {
			var out mediaAssetResp
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode media: %v", err)
			}
			if out.Data.Status == "ready" {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("media %s never became ready; last code %d body: %s", id, rec.Code, rec.Body.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func shareMoment(t *testing.T, h http.Handler, eventID, mediaAssetID, caption, token string) string {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/moments",
		fmt.Sprintf(`{"media_asset_id":%q,"caption":%q}`, mediaAssetID, caption), token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("share moment: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp momentResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode moment: %v", err)
	}
	if resp.Data.MediaAssetID != mediaAssetID {
		t.Fatalf("moment media mismatch: got %q want %q; body=%s", resp.Data.MediaAssetID, mediaAssetID, rec.Body.String())
	}
	return resp.Data.ID
}

func listMoments(t *testing.T, h http.Handler, eventID string) paginateResponse {
	t.Helper()
	return decodePaginated(t, doJSON(t, h, http.MethodGet, "/api/v1/events/"+eventID+"/moments", "", ""))
}

func listAttendees(t *testing.T, h http.Handler, eventID string) ([]attendeeItem, int) {
	t.Helper()
	rec := doJSON(t, h, http.MethodGet, "/api/v1/events/"+eventID+"/attendees", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list attendees: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data       []attendeeItem `json:"data"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode attendees: %v", err)
	}
	return resp.Data, resp.Pagination.Total
}

// TestMomentsLifecycle drives the full moments flow through the HTTP API:
// eligibility gating, media ownership, gallery pagination, admin/owner delete,
// and public_rsvp attendee privacy.
func TestMomentsLifecycle(t *testing.T) {
	app, h := newTestApp(t)
	cancel := startMediaWorker(t, app)
	defer cancel()

	orgTok, adminTok := promoteOrganizer(t, h, "momorg", fmt.Sprintf("Mom Org %d", time.Now().UnixNano()))
	eventID := createPublishedEvent(t, h, orgTok, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))

	_, userA, _ := registerAndToken(t, h, "momuserA")
	_, userB, _ := registerAndToken(t, h, "momuserB")

	png := tinyPNG(t)
	aAsset, _ := createUploadAndComplete(t, h, userA, "event_gallery", png)
	bAsset, _ := createUploadAndComplete(t, h, userB, "event_gallery", png)
	waitMediaReady(t, h, aAsset)
	waitMediaReady(t, h, bAsset)

	// No RSVP yet -> 403.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/moments",
		fmt.Sprintf(`{"media_asset_id":%q}`, aAsset), userA)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("share without RSVP: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	// userA RSVPs publicly and shares a moment.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/rsvp", `{"public_rsvp":true}`, userA)
	if rec.Code != http.StatusOK {
		t.Fatalf("rsvp A: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	momentID := shareMoment(t, h, eventID, aAsset, "first light", userA)

	// Gallery is public and paginated.
	g := listMoments(t, h, eventID)
	if g.Pagination.Total != 1 || len(g.Data) != 1 {
		t.Fatalf("expected 1 moment, got total=%d items=%d", g.Pagination.Total, len(g.Data))
	}
	if g.Data[0]["media_asset_id"] != aAsset {
		t.Fatalf("unexpected moment in gallery: %+v", g.Data[0])
	}

	// userB shares a moment during private RSVP: now eligible too.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/rsvp", `{"public_rsvp":false}`, userB)
	if rec.Code != http.StatusOK {
		t.Fatalf("rsvp B: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	bMomentID := shareMoment(t, h, eventID, bAsset, "second", userB)
	if g = listMoments(t, h, eventID); g.Pagination.Total != 2 {
		t.Fatalf("expected 2 moments, got %d", g.Pagination.Total)
	}

	// Using someone else's media asset is rejected.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/moments",
		fmt.Sprintf(`{"media_asset_id":%q}`, aAsset), userB)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("foreign media asset: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
	// Missing media_asset_id is invalid input.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/moments", `{}`, userB)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty media_asset_id: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// Attendee directory: only public_rsvp=confirmed attendees appear.
	attendees, total := listAttendees(t, h, eventID)
	if total != 1 || len(attendees) != 1 {
		t.Fatalf("expected 1 public attendee, got total=%d items=%d", total, len(attendees))
	}
	if attendees[0].UserID != userAUserIDOf(t, h, userA) {
		t.Fatalf("unexpected attendee: %+v", attendees[0])
	}

	// userB cannot delete userA's moment.
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/moments/"+momentID, "", userB)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-user delete: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	// Admin deletes userA's moment.
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/moments/"+momentID, "", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin delete: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Owner deletes their own moment.
	rec = doJSON(t, h, http.MethodDelete, "/api/v1/moments/"+bMomentID, "", userB)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner delete: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if g = listMoments(t, h, eventID); g.Pagination.Total != 0 {
		t.Fatalf("expected empty gallery after deletes, got %d", g.Pagination.Total)
	}
}

// userAUserIDOf resolves the me endpoint to get the user id of a token.
func userAUserIDOf(t *testing.T, h http.Handler, token string) string {
	t.Helper()
	rec := doJSON(t, h, http.MethodGet, "/api/v1/users/me", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("me: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	return resp.Data.ID
}

// TestMomentsNotFound: moments/attendees on a hidden/draft event are 404.
func TestMomentsNotFound(t *testing.T) {
	_, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "momng", fmt.Sprintf("Mom Ng %d", time.Now().UnixNano()))

	// Create a draft event (never published) and try the public moments endpoints.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/venues",
		fmt.Sprintf(`{"name":"Ng Venue %d","latitude":40.71,"longitude":-74.00,"city":"NYC"}`, time.Now().UnixNano()), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create venue: %d: %s", rec.Code, rec.Body.String())
	}
	venueID := idFromCreate(t, rec, "id")
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events",
		fmt.Sprintf(`{"title":"Ng Event %d","starts_at":%q,"venue_id":%q,"price_is_free":true}`,
			time.Now().UnixNano(), time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339), venueID), orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create event: %d: %s", rec.Code, rec.Body.String())
	}
	eventID := idFromCreate(t, rec, "id")

	for _, path := range []string{
		"/api/v1/events/" + eventID + "/moments",
		"/api/v1/events/" + eventID + "/attendees",
	} {
		if rec := doJSON(t, h, http.MethodGet, path, "", ""); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: expected 404 for draft event, got %d: %s", path, rec.Code, rec.Body.String())
		}
	}
}
