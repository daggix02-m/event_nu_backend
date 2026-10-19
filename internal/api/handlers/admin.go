package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/api"
	"github.com/daggix02-m/event_nu_backend/internal/api/dto"
	"github.com/daggix02-m/event_nu_backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu_backend/internal/domain"
	"github.com/daggix02-m/event_nu_backend/internal/service"
	"github.com/daggix02-m/event_nu_backend/internal/shared"
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
		out = append(out, h.adminEventDTO(r, e))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   result.Total,
		HasNext: page*limit < result.Total,
	})
}

// adminEventDTO builds the admin event row and resolves the poster/teaser
// media URLs (blank when the media is not attached or not yet ready). A media
// resolution hiccup must not fail the list; it is logged and left blank.
func (h *AdminHandlers) adminEventDTO(r *http.Request, e *domain.Event) dto.AdminEventDTO {
	out := dto.NewAdminEventDTO(e)
	if e.PosterMediaID == nil && e.TeaserMediaID == nil {
		return out
	}
	if h.app == nil || h.app.Media == nil {
		return out
	}
	poster, teaser, err := h.app.Media.ResolveEventMedia(r.Context(), e.PosterMediaID, e.TeaserMediaID)
	if err != nil {
		h.app.Logger.Warn("admin event media resolution unavailable", "event_id", e.ID, "error", err.Error())
		return out
	}
	out.PosterURL = poster
	out.TeaserURL = teaser
	return out
}

func (h *AdminHandlers) ListOrganizers(w http.ResponseWriter, r *http.Request) {
	if !h.isAdmin(r.Context()) {
		h.app.AppError(w, r, shared.NewAppError("forbidden", "Admin access required.", http.StatusForbidden))
		return
	}
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	result, err := h.admin.ListOrganizers(r.Context(), page, limit,
		r.URL.Query().Get("q"), r.URL.Query().Get("status"))
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.AdminOrganizerDTO, 0, len(result.Items))
	for _, o := range result.Items {
		out = append(out, h.adminOrganizerDTO(r, o))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   result.Total,
		HasNext: page*limit < result.Total,
	})
}

// GetOrganizer is the console's organizer detail: profile + resolved logo +
// total event count + the newest events across all lifecycle statuses.
func (h *AdminHandlers) GetOrganizer(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if !h.requireAdmin(w, r) {
		return
	}
	org, err := h.admin.GetOrganizer(r.Context(), id)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	eventCount, err := h.admin.OrganizerEventCount(r.Context(), id)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	recent, err := h.admin.RecentOrganizerEvents(r.Context(), id, 10)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, h.adminOrganizerDetailDTO(r, org, recent, eventCount))
}

// CreateOrganizer provisions a standalone or owner-linked organizer profile
// directly (no application queue involved).
func (h *AdminHandlers) CreateOrganizer(w http.ResponseWriter, r *http.Request) {
	if !h.isAdmin(r.Context()) {
		h.app.AppError(w, r, shared.NewAppError("forbidden", "Admin access required.", http.StatusForbidden))
		return
	}
	var req dto.CreateAdminOrganizerRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	org := &domain.Organizer{
		Name:        req.Name,
		Slug:        req.Slug,
		Bio:         req.Bio,
		OwnerUserID: req.OwnerUserID,
		LogoMediaID: req.LogoMediaID,
		Status:      req.Status,
	}
	created, err := h.admin.CreateOrganizer(r.Context(), middleware.UserID(r.Context()), org)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, h.adminOrganizerDetailDTO(r, created, nil, 0))
}

// adminOrganizerDTO builds the console list row, carrying the repo-derived
// event count and resolving the organizer logo URL.
func (h *AdminHandlers) adminOrganizerDTO(r *http.Request, o *domain.Organizer) dto.AdminOrganizerDTO {
	out := dto.NewAdminOrganizerDTO(o)
	out.EventCount = o.EventCount
	out.LogoURL = h.resolveOrganizerLogo(r, o.LogoMediaID, o.ID)
	return out
}

// adminOrganizerDetailDTO composes the console detail response.
func (h *AdminHandlers) adminOrganizerDetailDTO(r *http.Request, o *domain.Organizer, recent []*domain.Event, eventCount int) dto.AdminOrganizerDetailDTO {
	out := dto.AdminOrganizerDetailDTO{
		AdminOrganizerDTO: dto.NewAdminOrganizerDTO(o),
		RecentEvents:      make([]dto.AdminEventDTO, 0, len(recent)),
	}
	out.EventCount = eventCount
	out.LogoURL = h.resolveOrganizerLogo(r, o.LogoMediaID, o.ID)
	for _, e := range recent {
		out.RecentEvents = append(out.RecentEvents, h.adminEventDTO(r, e))
	}
	return out
}

func (h *AdminHandlers) resolveOrganizerLogo(r *http.Request, mediaID *string, organizerID string) string {
	if mediaID == nil || h.app == nil || h.app.Media == nil {
		return ""
	}
	logo, err := h.app.Media.ResolveOrganizerLogo(r.Context(), mediaID)
	if err != nil {
		h.app.Logger.Warn("admin organizer logo resolution unavailable", "organizer_id", organizerID, "error", err.Error())
		return ""
	}
	return logo
}

// ListUsers is the admin user directory: search (email/username), role/status
// filters and pagination.
func (h *AdminHandlers) ListUsers(w http.ResponseWriter, r *http.Request) {
	if !h.isAdmin(r.Context()) {
		h.app.AppError(w, r, shared.NewAppError("forbidden", "Admin access required.", http.StatusForbidden))
		return
	}
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	result, err := h.admin.ListUsers(r.Context(), page, limit,
		r.URL.Query().Get("q"), r.URL.Query().Get("role"), r.URL.Query().Get("status"))
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.AdminUserDTO, 0, len(result.Items))
	for _, u := range result.Items {
		out = append(out, dto.NewAdminUserDTO(u))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   result.Total,
		HasNext: page*limit < result.Total,
	})
}

// UpdateUser edits a user's directory record (username/bio/photo_url/role/
// status) with the self-demote/self-suspend guard applied in the service.
func (h *AdminHandlers) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if !h.requireAdmin(w, r) {
		return
	}
	var req dto.UpdateAdminUserRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	user, err := h.admin.UpdateUser(r.Context(), middleware.UserID(r.Context()), id,
		req.Username, req.Bio, req.PhotoURL, req.Role, req.Status)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, dto.NewAdminUserDTO(user))
}

// SuspendUser locks a user out of new logins (spec §10).
func (h *AdminHandlers) SuspendUser(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if !h.requireAdmin(w, r) {
		return
	}
	user, err := h.admin.SuspendUser(r.Context(), middleware.UserID(r.Context()), id)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, dto.NewAdminUserDTO(user))
}

// RestoreUser re-activates a suspended account (spec §10).
func (h *AdminHandlers) RestoreUser(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if !h.requireAdmin(w, r) {
		return
	}
	user, err := h.admin.RestoreUser(r.Context(), id)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, dto.NewAdminUserDTO(user))
}

// ListVenues is the console venue directory (GET /api/v1/admin/venues).
func (h *AdminHandlers) ListVenues(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	res, err := h.admin.ListVenues(r.Context(), page, limit, r.URL.Query().Get("q"), r.URL.Query().Get("status"))
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.AdminVenueDTO, 0, len(res.Items))
	for _, v := range res.Items {
		out = append(out, dto.NewAdminVenueDTO(v))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   res.Total,
		HasNext: page*limit < res.Total,
	})
}

// CreateVenue provisions a venue bound to an existing organizer (400 when the
// body is malformed).
func (h *AdminHandlers) CreateVenue(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	var req dto.CreateAdminVenueRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	v, err := h.admin.CreateVenue(r.Context(), req.OrganizerID, &domain.Venue{
		Name:        req.Name,
		Address:     req.Address,
		Latitude:    req.Latitude,
		Longitude:   req.Longitude,
		PlaceID:     req.PlaceID,
		City:        req.City,
		CountryCode: req.CountryCode,
	})
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, dto.NewAdminVenueDTO(v))
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

// GetEvent renders the console event detail: the admin row with resolved media
// URLs plus organizer identity and venue. Missing event → 404, but a missing
// organizer/venue is tolerated (rows render without them).
func (h *AdminHandlers) GetEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if !h.requireAdmin(w, r) {
		return
	}
	detail, err := h.admin.GetEventDetail(r.Context(), id)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := dto.AdminEventDetailDTO{AdminEventDTO: h.adminEventDTO(r, detail.Event)}
	if detail.Organizer != nil {
		out.Organizer = dto.AdminEventOrganizerSummary{
			ID:      detail.Organizer.ID,
			Slug:    detail.Organizer.Slug,
			Name:    detail.Organizer.Name,
			Status:  detail.Organizer.Status,
			LogoURL: h.resolveOrganizerLogo(r, detail.Organizer.LogoMediaID, detail.Organizer.ID),
		}
	}
	if detail.Venue != nil {
		venue := dto.NewAdminVenueDTO(detail.Venue)
		out.Venue = &venue
	}
	shared.WriteJSON(w, http.StatusOK, out)
}

// CreateEvent is the console's atomic event create: organizer resolve-or-create,
// venue resolve-or-create, insert, then publish (publish:false keeps a draft).
func (h *AdminHandlers) CreateEvent(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	in, err := parseAdminEventRequest(w, r)
	if err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	event, err := h.admin.CreateEvent(r.Context(), middleware.UserID(r.Context()), in)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, h.adminEventDTO(r, event))
}

// ReassignOrganizer moves an event to another organizer — by id, or by a new
// inline organizer auto-provisioned by the request.
func (h *AdminHandlers) ReassignOrganizer(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if !h.requireAdmin(w, r) {
		return
	}
	var req dto.ReassignOrganizerRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	in := &service.AdminOrganizerAssignInput{OrganizerID: req.OrganizerID}
	if req.Organizer != nil {
		in.NewOrganizer = adminOrganizerFromInput(req.Organizer)
	}
	event, err := h.admin.ReassignOrganizer(r.Context(), middleware.UserID(r.Context()), id, in)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, h.adminEventDTO(r, event))
}

// UpdateEvent edits any event's content. Same field rules as the public edit
// but without the organizer-ownership and draft-only gates (admin authority).
func (h *AdminHandlers) UpdateEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if !h.requireAdmin(w, r) {
		return
	}
	e, err := parseAdminEventUpdate(w, r)
	if err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	updated, err := h.admin.UpdateEventAdmin(r.Context(), id, e)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, h.adminEventDTO(r, updated))
}

// PublishEvent posts a draft or cancelled event (existing venue required).
func (h *AdminHandlers) PublishEvent(w http.ResponseWriter, r *http.Request) {
	h.writeAdminEventTransition(w, r, h.admin.PublishEventAdmin)
}

// CancelEvent pulls a draft or published event down.
func (h *AdminHandlers) CancelEvent(w http.ResponseWriter, r *http.Request) {
	h.writeAdminEventTransition(w, r, h.admin.CancelEventAdmin)
}

func (h *AdminHandlers) writeAdminEventTransition(w http.ResponseWriter, r *http.Request, fn func(context.Context, string) (*domain.Event, error)) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if !h.requireAdmin(w, r) {
		return
	}
	event, err := fn(r.Context(), id)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, h.adminEventDTO(r, event))
}

// adminOrganizerFromInput maps the inline organizer payload onto a domain
// organizer for the admin create/reassign paths.
func adminOrganizerFromInput(in *dto.AdminEventOrganizerInput) *domain.Organizer {
	o := &domain.Organizer{
		Name:        in.Name,
		Bio:         in.Bio,
		LogoMediaID: in.LogoMediaID,
		Status:      "active",
	}
	if in.Slug != nil {
		o.Slug = *in.Slug
	}
	return o
}

// parseAdminEventRequest decodes the atomic create payload into the service
// input, resolving RFC3339 timestamps like the public create endpoint.
func parseAdminEventRequest(w http.ResponseWriter, r *http.Request) (*service.AdminEventCreateInput, error) {
	var req dto.CreateAdminEventRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		return nil, err
	}
	in := &service.AdminEventCreateInput{
		OrganizerID: req.OrganizerID,
		VenueID:     req.VenueID,
		Publish:     true,
	}
	if req.Publish != nil {
		in.Publish = *req.Publish
	}
	if req.Organizer != nil {
		in.NewOrganizer = adminOrganizerFromInput(req.Organizer)
	}
	if req.Venue != nil {
		in.NewVenue = &domain.Venue{
			Name:        req.Venue.Name,
			Address:     req.Venue.Address,
			Latitude:    req.Venue.Latitude,
			Longitude:   req.Venue.Longitude,
			PlaceID:     req.Venue.PlaceID,
			City:        req.Venue.City,
			CountryCode: req.Venue.CountryCode,
		}
	}
	event, err := eventFromAdminFields(req)
	if err != nil {
		return nil, err
	}
	in.Event = event
	return in, nil
}

// parseAdminEventUpdate decodes the content-fields payload for the admin edit
// (organizer/venue/publish are not part of an edit).
func parseAdminEventUpdate(w http.ResponseWriter, r *http.Request) (*domain.Event, error) {
	var req dto.CreateAdminEventRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		return nil, err
	}
	return eventFromAdminFields(req)
}

// eventFromAdminFields maps the shared event form onto a domain event.
func eventFromAdminFields(req dto.CreateAdminEventRequest) (*domain.Event, error) {
	starts, err := time.Parse(time.RFC3339, req.StartsAt)
	if err != nil {
		return nil, errBadStartsAt
	}
	e := &domain.Event{
		VenueID:       req.VenueID,
		CategoryID:    req.CategoryID,
		Title:         req.Title,
		Description:   req.Description,
		StartsAt:      starts,
		PriceIsFree:   req.PriceIsFree,
		PriceDisplay:  req.PriceDisplay,
		ActionType:    req.ActionType,
		ActionTarget:  req.ActionTarget,
		MaxAttendees:  req.MaxAttendees,
		PosterMediaID: req.PosterMediaID,
		TeaserMediaID: req.TeaserMediaID,
	}
	if req.EndsAt != nil {
		ends, err := time.Parse(time.RFC3339, *req.EndsAt)
		if err != nil {
			return nil, errBadEndsAt
		}
		e.EndsAt = &ends
	}
	return e, nil
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
