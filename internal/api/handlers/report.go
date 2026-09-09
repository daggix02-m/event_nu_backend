package handlers

import (
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
	"github.com/daggix02-m/event_nu/backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu/backend/internal/service"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

type ReportHandlers struct {
	app     *api.Application
	reports *service.ReportService
}

func NewReportHandlers(app *api.Application, reports *service.ReportService) *ReportHandlers {
	return &ReportHandlers{app: app, reports: reports}
}

// ReportTarget handles POST /{entity}/{id}/report where entity is one of
// event|user|venue|comment. The entity type is fixed by the route, so the
// polymorphic validation in ReportService is dispatched statically.
func (h *ReportHandlers) ReportTarget(entityType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		targetID, ok := ParseUUIDParam(r, "id")
		if !ok {
			h.app.NotFound(w, r)
			return
		}
		var req dto.ReportRequest
		if err := shared.DecodeJSON(w, r, &req); err != nil {
			h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if err := h.reports.Create(r.Context(), middleware.UserID(r.Context()), entityType, targetID, req.ReasonCode, req.Description); err != nil {
			h.app.AppError(w, r, err)
			return
		}
		shared.WriteJSON(w, http.StatusCreated, dto.ReportResults{Reported: true})
	}
}
