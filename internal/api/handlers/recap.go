package handlers

import (
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
	"github.com/daggix02-m/event_nu/backend/internal/service"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

type RecapHandlers struct {
	app   *api.Application
	recap *service.RecapService
}

func NewRecapHandlers(app *api.Application, recap *service.RecapService) *RecapHandlers {
	return &RecapHandlers{app: app, recap: recap}
}

// Get returns the cached-or-regenerated recap for a published event.
func (h *RecapHandlers) Get(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	res, err := h.recap.Get(r.Context(), eventID)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, dto.ToRecapDTO(res))
}
