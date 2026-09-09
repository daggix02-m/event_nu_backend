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

type ReminderHandlers struct {
	app    *api.Application
	remind *service.ReminderService
}

func NewReminderHandlers(app *api.Application, remind *service.ReminderService) *ReminderHandlers {
	return &ReminderHandlers{app: app, remind: remind}
}

// Create sets a reminder ahead of a published event.
func (h *ReminderHandlers) Create(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	var req dto.ReminderRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	rem, err := h.remind.Create(r.Context(), middleware.UserID(r.Context()), id, req.RemindAt)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, dto.NewReminderDTO(&domain.ReminderWithEvent{
		Reminder: *rem,
	}))
}

// Delete removes the caller's reminder for an event (idempotent).
func (h *ReminderHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if err := h.remind.Delete(r.Context(), middleware.UserID(r.Context()), id); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, dto.NotificationReadResult{Read: true})
}

// My lists the caller's reminders.
func (h *ReminderHandlers) My(w http.ResponseWriter, r *http.Request) {
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	result, err := h.remind.MyReminders(r.Context(), middleware.UserID(r.Context()), page, limit)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.ReminderDTO, 0, len(result.Items))
	for _, rem := range result.Items {
		out = append(out, dto.NewReminderDTO(rem))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   result.Total,
		HasNext: page*limit < result.Total,
	})
}
