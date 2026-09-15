package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"path"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/storage"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// mediaAssetStore is the repository surface MediaService depends on (mirrors
// the other service stores).
type mediaAssetStore interface {
	CreateAsset(ctx context.Context, a *domain.MediaAsset) (*domain.MediaAsset, error)
	GetAssetByID(ctx context.Context, id string) (*domain.MediaAsset, error)
	SetStatus(ctx context.Context, id, status string, errMsg *string) error
	SoftDelete(ctx context.Context, id string) error
	EnqueueJob(ctx context.Context, mediaAssetID string) error
	ListVariants(ctx context.Context, mediaID string) ([]domain.MediaVariant, error)
	ListVariantsForMediaIDs(ctx context.Context, mediaIDs []string, variant, density string) (map[string]string, error)
}

// MediaService orchestrates the upload lifecycle: an upload intent creates a
// pending asset and a presigned PUT URL; the client uploads the bytes, then
// completes the intent to enqueue the processing job; the worker produces
// variants and flips the asset to ready.
type MediaService struct {
	assets  mediaAssetStore
	storage storage.ObjectStore
	cfg     config.Config
}

func NewMediaService(assets mediaAssetStore, store storage.ObjectStore, cfg config.Config) *MediaService {
	return &MediaService{assets: assets, storage: store, cfg: cfg}
}

func (s *MediaService) GetAssetByID(ctx context.Context, id string) (*domain.MediaAsset, error) {
	return s.assets.GetAssetByID(ctx, id)
}

var mediaContentTypes = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
	"image/webp": "webp",
}

var allowedMediaKinds = map[string]bool{
	domain.MediaKindEventPoster:   true,
	domain.MediaKindEventTeaser:   true,
	domain.MediaKindEventGallery:  true,
	domain.MediaKindOrganizerLogo: true,
	domain.MediaKindAvatar:        true,
	domain.MediaKindStory:         true,
}

// UploadIntent validates the request and returns the pending asset plus a
// presigned (or local) upload URL the client PUTs the bytes to.
func (s *MediaService) UploadIntent(ctx context.Context, userID, kind, contentType string, sizeBytes int64) (*domain.MediaAsset, string, error) {
	if !allowedMediaKinds[kind] {
		return nil, "", shared.NewAppError("invalid_media_kind",
			"kind must be one of event_poster, event_teaser, event_gallery, organizer_logo, avatar, story",
			http.StatusUnprocessableEntity)
	}
	ext, ok := mediaContentTypes[contentType]
	if !ok {
		return nil, "", shared.NewAppError("invalid_content_type",
			"content_type must be image/jpeg, image/png or image/webp",
			http.StatusUnprocessableEntity)
	}
	if sizeBytes <= 0 || sizeBytes > s.cfg.MediaMaxUploadBytes {
		return nil, "", shared.NewAppError("invalid_size",
			fmt.Sprintf("size_bytes must be between 1 and %d", s.cfg.MediaMaxUploadBytes),
			http.StatusUnprocessableEntity)
	}

	key := fmt.Sprintf("uploaders/%s/%s/original.%s", userID, newObjectToken(), ext)
	asset, err := s.assets.CreateAsset(ctx, &domain.MediaAsset{
		UploaderID:    userID,
		Kind:          kind,
		StorageBucket: s.storage.Bucket(),
		StorageKey:    key,
		ContentType:   contentType,
		Status:        domain.MediaStatusPending,
	})
	if err != nil {
		return nil, "", err
	}

	uploadURL, err := s.storage.PresignUpload(ctx, key, contentType, s.cfg.MediaUploadExpiry)
	if err != nil {
		return nil, "", fmt.Errorf("presign upload: %w", err)
	}
	return asset, uploadURL, nil
}

// CompleteUpload flips an owned, pending asset to processing and enqueues the
// worker job (atomically in the caller's transaction).
func (s *MediaService) CompleteUpload(ctx context.Context, userID, assetID string) (*domain.MediaAsset, error) {
	asset, err := s.assets.GetAssetByID(ctx, assetID)
	if err != nil {
		return nil, err
	}
	if asset.UploaderID != userID {
		return nil, shared.NewAppError("forbidden", "You can only complete your own uploads.", http.StatusForbidden)
	}
	if asset.Status != domain.MediaStatusPending {
		return nil, shared.NewAppError("media_already_completed", "This upload has already been processed.", http.StatusConflict)
	}
	if err := s.assets.SetStatus(ctx, assetID, domain.MediaStatusProcessing, nil); err != nil {
		return nil, err
	}
	if err := s.assets.EnqueueJob(ctx, assetID); err != nil {
		return nil, err
	}
	asset.Status = domain.MediaStatusProcessing
	return asset, nil
}

// GetAsset returns an asset with its variants. Visibility is enforced by RLS:
// owners see their own, the public sees ready, non-deleted assets.
func (s *MediaService) GetAsset(ctx context.Context, assetID string) (*domain.MediaAsset, []domain.MediaVariant, error) {
	asset, err := s.assets.GetAssetByID(ctx, assetID)
	if err != nil {
		return nil, nil, err
	}
	variants, err := s.assets.ListVariants(ctx, assetID)
	if err != nil {
		return nil, nil, err
	}
	return asset, variants, nil
}

// DeleteAsset soft-deletes an owned asset. Storage objects are removed
// best-effort (orphaned objects are harmless: variant URLs stop being served
// the moment the row disappears).
func (s *MediaService) DeleteAsset(ctx context.Context, userID, assetID string) error {
	asset, err := s.assets.GetAssetByID(ctx, assetID)
	if err != nil {
		return err
	}
	if asset.UploaderID != userID {
		return shared.NewAppError("forbidden", "You can only delete your own uploads.", http.StatusForbidden)
	}
	if err := s.assets.SoftDelete(ctx, assetID); err != nil {
		return err
	}
	for _, key := range []string{asset.StorageKey} {
		_ = s.storage.Delete(ctx, key)
	}
	variants, err := s.assets.ListVariants(ctx, assetID)
	if err == nil {
		for _, v := range variants {
			_ = s.storage.Delete(ctx, v.StorageKey)
		}
	}
	return nil
}

// ResolveEventMedia maps an event's poster/teaser media ids to served variant
// URLs (card@1x). A media id that is not yet ready simply resolves to "".
func (s *MediaService) ResolveEventMedia(ctx context.Context, posterID, teaserID *string) (posterURL, teaserURL string, err error) {
	var lookup []string
	for _, id := range []*string{posterID, teaserID} {
		if id != nil {
			lookup = append(lookup, *id)
		}
	}
	if len(lookup) == 0 {
		return "", "", nil
	}
	urls, err := s.assets.ListVariantsForMediaIDs(ctx, lookup, domain.MediaVariantCard, "1x")
	if err != nil {
		return "", "", err
	}
	if posterID != nil {
		posterURL = urls[*posterID]
	}
	if teaserID != nil {
		teaserURL = urls[*teaserID]
	}
	return posterURL, teaserURL, nil
}

func newObjectToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// VariantKey derives the storage key for a produced variant, placed next to
// the original so all objects belong to the same uploader prefix.
func VariantKey(originalKey, variant, density, format string) string {
	dir := path.Dir(originalKey)
	base := path.Base(originalKey)
	noExt := base[:len(base)-len(path.Ext(base))]
	return fmt.Sprintf("%s/%s-%s-%s.%s", dir, noExt, variant, density, format)
}
