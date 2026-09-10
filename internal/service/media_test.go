package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/storage"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

type stubMediaStore struct {
	onCreateAsset func(a *domain.MediaAsset) (*domain.MediaAsset, error)
	onGetByID     func(id string) (*domain.MediaAsset, error)
	onSetStatus   func(id, status string, errMsg *string) error
	onSoftDelete  func(id string) error
	onEnqueue     func(id string) error
	onVariants    func(id string) ([]domain.MediaVariant, error)
	onVariantURLs func(ids []string, variant, density string) (map[string]string, error)
}

func (s stubMediaStore) CreateAsset(ctx context.Context, a *domain.MediaAsset) (*domain.MediaAsset, error) {
	if s.onCreateAsset == nil {
		a.ID = "asset-1"
		return a, nil
	}
	return s.onCreateAsset(a)
}

func (s stubMediaStore) GetAssetByID(ctx context.Context, id string) (*domain.MediaAsset, error) {
	if s.onGetByID == nil {
		return nil, shared.ErrNotFound
	}
	return s.onGetByID(id)
}

func (s stubMediaStore) SetStatus(ctx context.Context, id, status string, errMsg *string) error {
	if s.onSetStatus == nil {
		return nil
	}
	return s.onSetStatus(id, status, errMsg)
}

func (s stubMediaStore) SoftDelete(ctx context.Context, id string) error {
	if s.onSoftDelete == nil {
		return nil
	}
	return s.onSoftDelete(id)
}

func (s stubMediaStore) EnqueueJob(ctx context.Context, id string) error {
	if s.onEnqueue == nil {
		return nil
	}
	return s.onEnqueue(id)
}

func (s stubMediaStore) ListVariants(ctx context.Context, id string) ([]domain.MediaVariant, error) {
	if s.onVariants == nil {
		return []domain.MediaVariant{}, nil
	}
	return s.onVariants(id)
}

func (s stubMediaStore) ListVariantsForMediaIDs(ctx context.Context, ids []string, variant, density string) (map[string]string, error) {
	if s.onVariantURLs == nil {
		return map[string]string{}, nil
	}
	return s.onVariantURLs(ids, variant, density)
}

type stubObjectStore struct {
	storage.ObjectStore
	bucket     string
	uploadURL  string
	presignErr error
	deleteErr  error
}

func (s stubObjectStore) Bucket() string { return s.bucket }
func (s stubObjectStore) PresignUpload(ctx context.Context, key, contentType string, expiry time.Duration) (string, error) {
	return s.uploadURL, s.presignErr
}
func (s stubObjectStore) Delete(ctx context.Context, key string) error { return s.deleteErr }

func newMediaService(store stubMediaStore, objects storage.ObjectStore) *MediaService {
	cfg := config.Config{
		MediaUploadExpiry:   time.Minute * 5,
		MediaMaxUploadBytes: 10 * 1024 * 1024,
	}
	return NewMediaService(store, objects, cfg)
}

func appErrCode(t *testing.T, err error) string {
	t.Helper()
	var ae *shared.AppError
	if !errors.As(err, &ae) {
		t.Fatalf("expected AppError, got %v", err)
	}
	return ae.Code
}

func TestUploadIntentValid(t *testing.T) {
	svc := newMediaService(stubMediaStore{}, stubObjectStore{bucket: "local", uploadURL: "https://up/u"})
	asset, uploadURL, err := svc.UploadIntent(context.Background(), "user-1", domain.MediaKindEventPoster, "image/webp", 1024)
	if err != nil {
		t.Fatalf("UploadIntent: %v", err)
	}
	if asset.ID == "" || asset.Kind != domain.MediaKindEventPoster || asset.Status != domain.MediaStatusPending {
		t.Fatalf("unexpected asset: %+v", asset)
	}
	if asset.UploaderID != "user-1" || asset.ContentType != "image/webp" {
		t.Fatalf("asset fields wrong: %+v", asset)
	}
	if asset.StorageKey == "" || asset.StorageBucket != "local" {
		t.Fatalf("storage fields wrong: %+v", asset)
	}
	if uploadURL != "https://up/u" {
		t.Fatalf("upload URL: %s", uploadURL)
	}
}

func TestUploadIntentValidation(t *testing.T) {
	svc := newMediaService(stubMediaStore{}, stubObjectStore{bucket: "local"})
	upload := func(kind, ct string, size int64) error {
		_, _, err := svc.UploadIntent(context.Background(), "u", kind, ct, size)
		return err
	}
	if code := appErrCode(t, upload("bogus_kind", "image/webp", 1)); code != "invalid_media_kind" {
		t.Fatalf("kind: %s", code)
	}
	if code := appErrCode(t, upload("avatar", "video/mp4", 1)); code != "invalid_content_type" {
		t.Fatalf("content type: %s", code)
	}
	if code := appErrCode(t, upload("avatar", "image/jpeg", 0)); code != "invalid_size" {
		t.Fatalf("size zero: %s", code)
	}
	if code := appErrCode(t, upload("avatar", "image/jpeg", 11*1024*1024)); code != "invalid_size" {
		t.Fatalf("size too big: %s", code)
	}
}

func TestCompleteUploadOwnershipAndStatus(t *testing.T) {
	owner := &domain.MediaAsset{ID: "a", UploaderID: "user-1", Status: domain.MediaStatusPending}
	svc := newMediaService(stubMediaStore{
		onGetByID: func(id string) (*domain.MediaAsset, error) { return owner, nil },
		onSetStatus: func(id, status string, errMsg *string) error {
			owner.Status = status
			return nil
		},
	}, stubObjectStore{})

	complete := func(user string) error {
		_, err := svc.CompleteUpload(context.Background(), user, "a")
		return err
	}

	// Not the owner.
	if code := appErrCode(t, complete("user-2")); code != "forbidden" {
		t.Fatalf("foreign upload: %s", code)
	}

	// Not pending.
	owner.Status = domain.MediaStatusProcessing
	if code := appErrCode(t, complete("user-1")); code != "media_already_completed" {
		t.Fatalf("already processing: %s", code)
	}
}

func TestCompleteUploadEnqueues(t *testing.T) {
	owner := &domain.MediaAsset{ID: "a", UploaderID: "user-1", Status: domain.MediaStatusPending}
	var enqueued string
	svc := newMediaService(stubMediaStore{
		onGetByID: func(id string) (*domain.MediaAsset, error) { return owner, nil },
		onSetStatus: func(id, status string, errMsg *string) error {
			owner.Status = status
			return nil
		},
		onEnqueue: func(id string) error { enqueued = id; return nil },
	}, stubObjectStore{})

	out, err := svc.CompleteUpload(context.Background(), "user-1", "a")
	if err != nil {
		t.Fatalf("CompleteUpload: %v", err)
	}
	if out.Status != domain.MediaStatusProcessing || enqueued != "a" {
		t.Fatalf("unexpected result: status=%s enqueued=%s", out.Status, enqueued)
	}
}

func TestDeleteAssetOwnership(t *testing.T) {
	asset := &domain.MediaAsset{ID: "a", UploaderID: "user-1", StorageKey: "up"}
	gone := false

	svc := newMediaService(stubMediaStore{
		onGetByID: func(id string) (*domain.MediaAsset, error) {
			if gone {
				return nil, shared.ErrNotFound
			}
			return asset, nil
		},
		onSoftDelete: func(id string) error { gone = true; return nil },
	}, stubObjectStore{})

	if code := appErrCode(t, svc.DeleteAsset(context.Background(), "user-2", "a")); code != "forbidden" {
		t.Fatalf("foreign delete: %s", code)
	}
	if err := svc.DeleteAsset(context.Background(), "user-1", "a"); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if _, _, err := svc.GetAsset(context.Background(), "a"); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("missing asset should 404: %v", err)
	}
}

func TestResolveEventMedia(t *testing.T) {
	svc := newMediaService(stubMediaStore{
		onVariantURLs: func(ids []string, variant, density string) (map[string]string, error) {
			out := map[string]string{}
			for _, id := range ids {
				out[id] = "https://cdn/" + id
			}
			return out, nil
		},
	}, stubObjectStore{})

	poster := "p1"
	teaser := "t1"
	posterURL, teaserURL, err := svc.ResolveEventMedia(context.Background(), &poster, &teaser)
	if err != nil {
		t.Fatalf("ResolveEventMedia: %v", err)
	}
	if posterURL != "https://cdn/p1" || teaserURL != "https://cdn/t1" {
		t.Fatalf("urls: %q %q", posterURL, teaserURL)
	}

	// Nil ids short-circuit without querying.
	if _, _, err := svc.ResolveEventMedia(context.Background(), nil, nil); err != nil {
		t.Fatalf("nil ids: %v", err)
	}
}

func TestVariantKey(t *testing.T) {
	got := VariantKey("uploaders/u/a/original.jpg", "card", "2x", "webp")
	want := "uploaders/u/a/original-card-2x.webp"
	if got != want {
		t.Fatalf("VariantKey: got %q want %q", got, want)
	}
}
