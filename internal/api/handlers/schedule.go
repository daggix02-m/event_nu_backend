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

type ScheduleHandlers struct {
	app      *api.Application
	schedule *service.ScheduleService
}

func NewScheduleHandlers(app *api.Application, schedule *service.ScheduleService) *ScheduleHandlers {
	return &ScheduleHandlers{app: app, schedule: schedule}
}

// Create adds a schedule slot to an event (organizer-only).
func (h *ScheduleHandlers) Create(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	var req dto.ScheduleRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	sess, err := h.schedule.Create(r.Context(), middleware.UserID(r.Context()), eventID, fromScheduleReq(req))
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, toSessionDTO(sess))
}

// Update edits one schedule slot (organizer-only).
func (h *ScheduleHandlers) Update(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	var req dto.ScheduleRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	sess, err := h.schedule.Update(r.Context(), middleware.UserID(r.Context()), sessionID, fromScheduleReq(req))
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, toSessionDTO(sess))
}

// Delete removes one schedule slot (organizer-only).
func (h *ScheduleHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if err := h.schedule.Delete(r.Context(), middleware.UserID(r.Context()), sessionID); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// List returns the schedule for a published event, ordered by start time.
func (h *ScheduleHandlers) List(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	items, err := h.schedule.List(r.Context(), eventID)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.ScheduleDTO, 0, len(items))
	for _, s := range items {
		out = append(out, toSessionDTO(s))
	}
	shared.WriteJSON(w, http.StatusOK, out)
}

func fromScheduleReq(req dto.ScheduleRequest) *domain.EventSession {
	return &domain.EventSession{
		Title:    req.Title,
		Speaker:  nilIfEmpty(req.Speaker),
		Stage:    nilIfEmpty(req.Stage),
		StartsAt: req.StartsAt,
		EndsAt:   req.EndsAt,
	}
}

func toSessionDTO(s *domain.EventSession) dto.ScheduleDTO {
	return dto.ScheduleDTO{
		ID:        s.ID,
		EventID:   s.EventID,
		Title:     s.Title,
		Speaker:   s.Speaker,
		Stage:     s.Stage,
		StartsAt:  s.StartsAt,
		EndsAt:    s.EndsAt,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}
}

// nilIfEmpty converts an empty string from the client into a NULL column,
// while preserving non-empty values (nullable speaker/stage).
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
