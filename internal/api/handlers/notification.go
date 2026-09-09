package handlers

import (
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
	"github.com/daggix02-m/event_nu/backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu/backend/internal/service"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

type NotificationHandlers struct {
	app   *api.Application
	notes *service.NotificationService
}

func NewNotificationHandlers(app *api.Application, notes *service.NotificationService) *NotificationHandlers {
	return &NotificationHandlers{app: app, notes: notes}
}

// List returns the caller's inbox, newest first.
func (h *NotificationHandlers) List(w http.ResponseWriter, r *http.Request) {
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	result, err := h.notes.List(r.Context(), middleware.UserID(r.Context()), page, limit)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.NotificationDTO, 0, len(result.Items))
	for _, n := range result.Items {
		out = append(out, dto.NewNotificationDTO(n))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   result.Total,
		HasNext: page*limit < result.Total,
	})
}

// Read marks a single notification read (idempotent).
func (h *NotificationHandlers) Read(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if err := h.notes.Read(r.Context(), middleware.UserID(r.Context()), id); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, dto.NotificationReadResult{Read: true})
}

// ReadAll marks every notification read and returns how many changed.
func (h *NotificationHandlers) ReadAll(w http.ResponseWriter, r *http.Request) {
	count, err := h.notes.ReadAll(r.Context(), middleware.UserID(r.Context()))
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, dto.NotificationReadAllResult{Updated: count})
}
