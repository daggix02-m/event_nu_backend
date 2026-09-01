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

type OrganizerHandlers struct {
	app *api.Application
	org *service.OrganizerService
}

func NewOrganizerHandlers(app *api.Application, org *service.OrganizerService) *OrganizerHandlers {
	return &OrganizerHandlers{app: app, org: org}
}

func (h *OrganizerHandlers) Apply(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	var req dto.ApplyOrganizerRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	app, err := h.org.Apply(r.Context(), userID, req.RequestedName, req.RequestedSlug, req.Bio)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, toApplicationDTO(app))
}

func (h *OrganizerHandlers) GetMyApplication(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	app, err := h.org.GetMyApplication(r.Context(), userID)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, toApplicationDTO(app))
}

func (h *OrganizerHandlers) Approve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req dto.ReviewApplicationRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	org, err := h.org.Approve(r.Context(), id, middleware.UserID(r.Context()), req.Notes)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"organizer_id": org.ID})
}

func (h *OrganizerHandlers) Reject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req dto.ReviewApplicationRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	if err := h.org.Reject(r.Context(), id, middleware.UserID(r.Context()), req.Notes); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

func toApplicationDTO(a *domain.OrganizerApplication) dto.OrganizerApplicationDTO {
	return dto.OrganizerApplicationDTO{
		ID:            a.ID,
		RequestedName: a.RequestedName,
		RequestedSlug: a.RequestedSlug,
		Bio:           a.Bio,
		Status:        a.Status,
		ReviewNotes:   a.ReviewNotes,
		CreatedAt:     a.CreatedAt,
	}
}