package handlers

import (
	"context"
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
	"github.com/daggix02-m/event_nu/backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/service"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// AdminHandlers serve the moderation surface: the report review queue and
// explicit event block/restore. Routes are mounted under the admin guard, so
// every handler re-verifies the caller is an admin before acting.
type AdminHandlers struct {
	app   *api.Application
	admin *service.AdminService
}

func NewAdminHandlers(app *api.Application, admin *service.AdminService) *AdminHandlers {
	return &AdminHandlers{app: app, admin: admin}
}

// isAdmin reports whether the caller holds the admin role. Mirrors the review
// handler's check; middleware.Role(ctx) is only the RLS role, not the user role.
func (h *AdminHandlers) isAdmin(ctx context.Context) bool {
	userID := middleware.UserID(ctx)
	if userID == "" {
		return false
	}
	u, err := h.app.Auth.GetUser(ctx, userID)
	if err != nil {
		return false
	}
	return u.Role == "admin"
}

func (h *AdminHandlers) ListReports(w http.ResponseWriter, r *http.Request) {
	if !h.isAdmin(r.Context()) {
		h.app.AppError(w, r, shared.NewAppError("forbidden", "Admin access required.", http.StatusForbidden))
		return
	}
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	result, err := h.admin.ListReports(r.Context(), page, limit)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.AdminReportDTO, 0, len(result.Items))
	for _, rep := range result.Items {
		out = append(out, dto.NewAdminReportDTO(rep))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   result.Total,
		HasNext: page*limit < result.Total,
	})
}

func (h *AdminHandlers) ListApplications(w http.ResponseWriter, r *http.Request) {
	if !h.isAdmin(r.Context()) {
		h.app.AppError(w, r, shared.NewAppError("forbidden", "Admin access required.", http.StatusForbidden))
		return
	}
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	result, err := h.admin.ListApplications(r.Context(), page, limit, r.URL.Query().Get("status"))
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.AdminOrganizerApplicationDTO, 0, len(result.Items))
	for _, app := range result.Items {
		out = append(out, dto.NewAdminOrganizerApplicationDTO(app))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   result.Total,
		HasNext: page*limit < result.Total,
	})
}

func (h *AdminHandlers) ListEvents(w http.ResponseWriter, r *http.Request) {
	if !h.isAdmin(r.Context()) {
		h.app.AppError(w, r, shared.NewAppError("forbidden", "Admin access required.", http.StatusForbidden))
		return
	}
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	result, err := h.admin.ListEvents(r.Context(), page, limit,
		r.URL.Query().Get("status"), r.URL.Query().Get("moderation_status"))
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.AdminEventDTO, 0, len(result.Items))
	for _, e := range result.Items {
		out = append(out, dto.NewAdminEventDTO(e))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   result.Total,
		HasNext: page*limit < result.Total,
	})
}

func (h *AdminHandlers) ResolveReport(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if !h.requireAdmin(w, r) {
		return
	}
	var req dto.ResolveRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	report, err := h.admin.ResolveReport(r.Context(), id, req.Resolution)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, dto.NewAdminReportDTO(report))
}

// BlockEvent hides an event from public surfaces (moderation only).
func (h *AdminHandlers) BlockEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if !h.requireAdmin(w, r) {
		return
	}
	h.writeModeratedEvent(w, r, id, h.admin.BlockEvent)
}

// RestoreEvent clears an event's moderation block.
func (h *AdminHandlers) RestoreEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if !h.requireAdmin(w, r) {
		return
	}
	h.writeModeratedEvent(w, r, id, h.admin.RestoreEvent)
}

// requireAdmin writes a 403 and returns false when the caller is not admin.
func (h *AdminHandlers) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if h.isAdmin(r.Context()) {
		return true
	}
	h.app.AppError(w, r, shared.NewAppError("forbidden", "Admin access required.", http.StatusForbidden))
	return false
}

func (h *AdminHandlers) writeModeratedEvent(w http.ResponseWriter, r *http.Request, id string, fn func(context.Context, string) (*domain.Event, error)) {
	event, err := fn(r.Context(), id)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, toEventDTO(event))
}
