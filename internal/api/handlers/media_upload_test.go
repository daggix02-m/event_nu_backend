package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/storage"
	"github.com/daggix02-m/event_nu/backend/internal/repository"
	"github.com/daggix02-m/event_nu/backend/internal/service"
	"github.com/daggix02-m/event_nu/backend/internal/worker"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

type mediaIntentResp struct {
	Data dto.UploadIntentResponse `json:"data"`
}

type mediaAssetResp struct {
	Data dto.MediaAssetDTO `json:"data"`
}

// objectKeyFromURL extracts the storage key from a local public URL, e.g.
// http://localhost:8080/media/objects/uploaders%2Fu%2Ftok%2Foriginal.png.
func objectKeyFromURL(t *testing.T, u string) string {
	t.Helper()
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("parse upload url %q: %v", u, err)
	}
	rel, ok := strings.CutPrefix(parsed.Path, "/media/objects/")
	if !ok {
		t.Fatalf("unexpected upload url %q", u)
	}
	key, err := url.PathUnescape(rel)
	if err != nil {
		t.Fatalf("unescape key %q: %v", rel, err)
	}
	return key
}

func createUploadAndComplete(t *testing.T, h http.Handler, tok, kind string, pngBytes []byte) (assetID, origKey string) {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/api/v1/media/upload-intents",
		fmt.Sprintf(`{"kind":%q,"content_type":"image/png","size_bytes":%d}`, kind, len(pngBytes)), tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload intent: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var intent mediaIntentResp
	if err := json.Unmarshal(rec.Body.Bytes(), &intent); err != nil {
		t.Fatalf("decode intent: %v", err)
	}
	if intent.Data.Status != "pending" || intent.Data.UploadURL == "" {
		t.Fatalf("unexpected intent: %+v", intent.Data)
	}

	origKey = objectKeyFromURL(t, intent.Data.UploadURL)

	u, err := url.Parse(intent.Data.UploadURL)
	if err != nil {
		t.Fatalf("parse upload url: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, u.Path, bytes.NewReader(pngBytes))
	req.Header.Set("Content-Type", "image/png")
	putRec := httptest.NewRecorder()
	h.ServeHTTP(putRec, req)
	if putRec.Code != http.StatusNoContent {
		t.Fatalf("PUT object: expected 204, got %d: %s", putRec.Code, putRec.Body.String())
	}

	rec = doJSON(t, h, http.MethodPost, "/api/v1/media/"+intent.Data.ID+"/complete", "", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete upload: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	return intent.Data.ID, origKey
}

// TestMediaUploadLifecycle drives the full local pipeline through the HTTP API:
// upload intent -> PUT object -> complete -> worker variant processing ->
// ready asset, then event attachment and public URL resolution.
func TestMediaUploadLifecycle(t *testing.T) {
	app, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "med", fmt.Sprintf("Media Org %d", time.Now().UnixNano()))
	pngBytes := tinyPNG(t)

	posterID, posterKey := createUploadAndComplete(t, h, orgTok, "event_poster", pngBytes)
	teaserID, teaserKey := createUploadAndComplete(t, h, orgTok, "event_teaser", pngBytes)

	// Run the media consumer against the same DB under the privileged 'service'
	// role, polling fast so the test finishes quickly.
	dsn := os.Getenv("DATABASE_URL")
	pool, err := database.NewPoolWithRole(context.Background(), dsn, "service")
	if err != nil {
		t.Fatalf("service pool: %v", err)
	}
	defer pool.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workerCfg := app.Config
	workerCfg.MediaJobPollInterval = 50 * time.Millisecond
	consumer := worker.NewMediaConsumer(testLogger(), workerCfg, repository.NewMediaRepository(pool), app.Storage)
	go func() { _ = consumer.Run(ctx) }()

	waitReady := func(id string) dto.MediaAssetDTO {
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
					return out.Data
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("media %s never became ready; last code %d body: %s", id, rec.Code, rec.Body.String())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	poster := waitReady(posterID)
	_ = waitReady(teaserID)
	ll := app.Storage.(*storage.Local)

	if len(poster.Variants) != 9 {
		t.Fatalf("expected 9 poster variants, got %d", len(poster.Variants))
	}
	var card *dto.MediaVariantDTO
	for i := range poster.Variants {
		v := &poster.Variants[i]
		if v.Variant == "card" && v.Density == "1x" {
			card = v
		}
		key := objectKeyFromURL(t, v.URL)
		if v.URL != ll.PublicReadURL(key) {
			t.Fatalf("variant url %q != public url %q", v.URL, ll.PublicReadURL(key))
		}
		req := httptest.NewRequest(http.MethodGet, path.Join("/media/objects", key), nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Fatalf("variant %s unservable: code %d", v.URL, rec.Code)
		}
		if v.Width == 0 || v.ByteSize == 0 {
			t.Fatalf("variant missing dims/size: %+v", v)
		}
	}
	if card == nil {
		t.Fatalf("card@1x variant missing: %+v", poster.Variants)
	}
	if poster.ContentType != "image/png" {
		t.Fatalf("content type: %s", poster.ContentType)
	}

	// Attach both at creation, publish, and confirm public URL resolution.
	start := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second).Format(time.RFC3339)
	recV := doJSON(t, h, http.MethodPost, "/api/v1/venues",
		fmt.Sprintf(`{"name":"Media Venue %d","latitude":40.71,"longitude":-74.00,"city":"NYC"}`, time.Now().UnixNano()), orgTok)
	if recV.Code != http.StatusCreated {
		t.Fatalf("create venue: %d %s", recV.Code, recV.Body.String())
	}
	venueID := idFromCreate(t, recV, "id")
	recE := doJSON(t, h, http.MethodPost, "/api/v1/events",
		fmt.Sprintf(`{"title":"Media Event","starts_at":%q,"venue_id":%q,"price_is_free":true,"poster_media_id":%q,"teaser_media_id":%q}`,
			start, venueID, posterID, teaserID), orgTok)
	if recE.Code != http.StatusCreated {
		t.Fatalf("create event: %d %s", recE.Code, recE.Body.String())
	}
	eventID := idFromCreate(t, recE, "id")
	recP := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/publish", "", orgTok)
	if recP.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", recP.Code, recP.Body.String())
	}

	recD := doJSON(t, h, http.MethodGet, "/api/v1/events/"+eventID, "", "")
	if recD.Code != http.StatusOK {
		t.Fatalf("get event: %d %s", recD.Code, recD.Body.String())
	}
	var ev struct {
		Data dto.EventDTO `json:"data"`
	}
	if err := json.Unmarshal(recD.Body.Bytes(), &ev); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	cardFormat := "webp"
	if card.Format != "" {
		cardFormat = card.Format
	}
	wantPoster := ll.PublicReadURL(service.VariantKey(posterKey, "card", "1x", cardFormat))
	wantTeaser := ll.PublicReadURL(service.VariantKey(teaserKey, "card", "1x", cardFormat))
	if ev.Data.PosterURL != wantPoster {
		t.Fatalf("poster_url: got %q want %q", ev.Data.PosterURL, wantPoster)
	}
	if ev.Data.TeaserURL != wantTeaser {
		t.Fatalf("teaser_url: got %q want %q", ev.Data.TeaserURL, wantTeaser)
	}
}

// TestMediaOwnershipBoundaries verifies RLS gating: a foreign user cannot see a
// peer's pending asset, and uploads reject foreign kinds.
func TestMediaOwnershipBoundaries(t *testing.T) {
	_, h := newTestApp(t)
	_, userTok, _ := registerAndToken(t, h, "meb")
	_, otherTok, _ := registerAndToken(t, h, "meo")
	pngBytes := tinyPNG(t)

	// Upload intent with a kind that is not allowed for a regular user.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/media/upload-intents",
		fmt.Sprintf(`{"kind":"xxx","content_type":"image/png","size_bytes":%d}`, len(pngBytes)), userTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad kind: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	posterID, _ := createUploadAndComplete(t, h, userTok, "event_poster", pngBytes)

	// Other user cannot see the pending asset (it becomes public only when ready).
	rec = doJSON(t, h, http.MethodGet, "/api/v1/media/"+posterID, "", otherTok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign pending read: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	// Owner sees it pending.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/media/"+posterID, "", userTok)
	if rec.Code != http.StatusOK || rec.Body.String() == "" {
		t.Fatalf("owner pending read: expected 200, got %d", rec.Code)
	}
	// Other user cannot delete (the pending asset is invisible to them, so
	// the API stays opaque and reports 404 rather than leaking existence).
	if rec = doJSON(t, h, http.MethodDelete, "/api/v1/media/"+posterID, "", otherTok); rec.Code != http.StatusNotFound {
		t.Fatalf("foreign delete: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	// Owner deletes fine.
	if rec = doJSON(t, h, http.MethodDelete, "/api/v1/media/"+posterID, "", userTok); rec.Code != http.StatusNoContent {
		t.Fatalf("owner delete: expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}
