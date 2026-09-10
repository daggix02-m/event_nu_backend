package handlers

import (
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
	"github.com/daggix02-m/event_nu/backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/service"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

type MediaHandlers struct {
	app   *api.Application
	media *service.MediaService
}

func NewMediaHandlers(app *api.Application, media *service.MediaService) *MediaHandlers {
	return &MediaHandlers{app: app, media: media}
}

// CreateUploadIntent validates the caller's upload and hands back a presigned
// PUT URL. No bytes move through the API; the client uploads directly to
// storage, then calls CompleteUpload.
func (h *MediaHandlers) CreateUploadIntent(w http.ResponseWriter, r *http.Request) {
	var req dto.UploadIntentRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	asset, uploadURL, err := h.media.UploadIntent(r.Context(), middleware.UserID(r.Context()), req.Kind, req.ContentType, req.SizeBytes)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, dto.UploadIntentResponse{
		ID:                  asset.ID,
		Kind:                asset.Kind,
		Status:              asset.Status,
		UploadURL:           uploadURL,
		UploadExpirySeconds: int(h.app.Config.MediaUploadExpiry.Seconds()),
	})
}

// CompleteUpload marks an owned, pending upload as processing and enqueues the
// worker job.
func (h *MediaHandlers) CompleteUpload(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	asset, err := h.media.CompleteUpload(r.Context(), middleware.UserID(r.Context()), id)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, toMediaAssetDTO(asset))
}

// GetMedia returns an asset and its variants. RLS gates visibility: owners see
// their own, everyone sees ready (non-deleted) assets.
func (h *MediaHandlers) GetMedia(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	asset, variants, err := h.media.GetAsset(r.Context(), id)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := toMediaAssetDTO(asset)
	for _, v := range variants {
		out.Variants = append(out.Variants, toMediaVariantDTO(v))
	}
	shared.WriteJSON(w, http.StatusOK, out)
}

// DeleteMedia soft-deletes an owned asset (owner-only).
func (h *MediaHandlers) DeleteMedia(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if err := h.media.DeleteAsset(r.Context(), middleware.UserID(r.Context()), id); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toMediaAssetDTO(a *domain.MediaAsset) dto.MediaAssetDTO {
	return dto.MediaAssetDTO{
		ID:            a.ID,
		Kind:          a.Kind,
		Status:        a.Status,
		ContentType:   a.ContentType,
		StorageBucket: a.StorageBucket,
		Width:         a.Width,
		Height:        a.Height,
		ByteSize:      a.ByteSize,
		ErrorMessage:  a.ErrorMessage,
		CreatedAt:     a.CreatedAt,
	}
}

func toMediaVariantDTO(v domain.MediaVariant) dto.MediaVariantDTO {
	var size int64
	if v.ByteSize != nil {
		size = *v.ByteSize
	}
	return dto.MediaVariantDTO{
		Variant:  v.Variant,
		Density:  v.Density,
		Format:   v.Format,
		Width:    v.Width,
		Height:   v.Height,
		URL:      v.CDNURL,
		ByteSize: size,
	}
}
