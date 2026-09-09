package handlers

import (
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
	"github.com/daggix02-m/event_nu/backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu/backend/internal/service"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

type ShareHandlers struct {
	app    *api.Application
	shares *service.ShareService
}

func NewShareHandlers(app *api.Application, shares *service.ShareService) *ShareHandlers {
	return &ShareHandlers{app: app, shares: shares}
}

// RecordShare records a share-analytics row for a visible event. Idempotent
// per (event, channel, ref).
func (h *ShareHandlers) RecordShare(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	var req dto.ShareEventRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := h.shares.RecordShare(r.Context(), middleware.UserID(r.Context()), eventID, req.Channel, req.Ref); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, map[string]string{"status": "recorded"})
}
