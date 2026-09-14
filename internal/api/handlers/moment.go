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

type MomentHandlers struct {
	app    *api.Application
	moment *service.MomentService
}

func NewMomentHandlers(app *api.Application, moment *service.MomentService) *MomentHandlers {
	return &MomentHandlers{app: app, moment: moment}
}

// Create adds a moment to an event. Idempotency-Key is optional but recommended.
func (h *MomentHandlers) Create(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	var req dto.CreateMomentRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.MediaAssetID == "" {
		h.app.ClientError(w, r, http.StatusUnprocessableEntity, "validation_error", "media_asset_id is required")
		return
	}
	moment, err := h.moment.Create(r.Context(), middleware.UserID(r.Context()), eventID, req.MediaAssetID, req.Caption)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, toMomentDTO(moment))
}

// List returns the paginated moments gallery for a published event.
func (h *MomentHandlers) List(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	result, err := h.moment.List(r.Context(), eventID, page, limit)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.MomentDTO, 0, len(result.Items))
	for _, item := range result.Items {
		out = append(out, toMomentDTO(item))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   result.Total,
		HasNext: page*limit < result.Total,
	})
}

// Delete removes a moment. Own moments by anyone; admins may remove any.
func (h *MomentHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	userID := middleware.UserID(r.Context())
	admin := false
	if u, err := h.app.Auth.GetUser(r.Context(), userID); err == nil && string(u.Role) == "admin" {
		admin = true
	}
	if err := h.moment.Delete(r.Context(), userID, id, admin); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ListAttendees returns the paginated public_rsvp attendee directory.
func (h *MomentHandlers) ListAttendees(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	result, err := h.moment.ListAttendees(r.Context(), eventID, page, limit)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.MomentAttendeeDTO, 0, len(result.Items))
	for _, item := range result.Items {
		out = append(out, toMomentAttendeeDTO(item))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   result.Total,
		HasNext: page*limit < result.Total,
	})
}

func toMomentDTO(m *domain.Moment) dto.MomentDTO {
	return dto.MomentDTO{
		ID:           m.ID,
		EventID:      m.EventID,
		UserID:       m.UserID,
		MediaAssetID: m.MediaAssetID,
		Caption:      m.Caption,
		CreatedAt:    m.CreatedAt,
	}
}

func toMomentAttendeeDTO(a *domain.MomentAttendee) dto.MomentAttendeeDTO {
	return dto.MomentAttendeeDTO{
		UserID:      a.UserID,
		DisplayName: a.DisplayName,
		AvatarURL:   a.AvatarURL,
		CreatedAt:   a.CreatedAt,
	}
}
