package handlers

import (
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
	"github.com/daggix02-m/event_nu/backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu/backend/internal/service"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

type FollowHandlers struct {
	app     *api.Application
	follows *service.FollowService
}

func NewFollowHandlers(app *api.Application, follows *service.FollowService) *FollowHandlers {
	return &FollowHandlers{app: app, follows: follows}
}

func (h *FollowHandlers) GetOrganizer(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	org, err := h.follows.GetOrganizer(r.Context(), id)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	h.writeOrganizerProfile(w, r, org.ID)
}

// writeOrganizerProfile resolves follow aggregates and writes the organizer DTO.
func (h *FollowHandlers) writeOrganizerProfile(w http.ResponseWriter, r *http.Request, organizerID string) {
	org, count, followed, err := h.follows.Profile(r.Context(), organizerID, middleware.UserID(r.Context()))
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, dto.OrganizerDTO{
		ID:            org.ID,
		Slug:          org.Slug,
		Name:          org.Name,
		Bio:           org.Bio,
		Status:        org.Status,
		FollowerCount: count,
		FollowedByMe:  followed,
		CreatedAt:     org.CreatedAt,
	})
}

func (h *FollowHandlers) Follow(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if _, err := h.follows.Follow(r.Context(), middleware.UserID(r.Context()), id); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	h.writeFollowState(w, r, id)
}

func (h *FollowHandlers) Unfollow(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if err := h.follows.Unfollow(r.Context(), middleware.UserID(r.Context()), id); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	h.writeFollowState(w, r, id)
}

func (h *FollowHandlers) writeFollowState(w http.ResponseWriter, r *http.Request, organizerID string) {
	total, followed, err := h.follows.State(r.Context(), organizerID, middleware.UserID(r.Context()))
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, dto.FollowState{FollowerCount: total, FollowedByMe: followed})
}
