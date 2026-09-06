package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
	"github.com/daggix02-m/event_nu/backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/service"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

type EventHandlers struct {
	app   *api.Application
	event *service.EventService
}

var (
	errBadStartsAt = errBadTime("starts_at")
	errBadEndsAt   = errBadTime("ends_at")
)

func errBadTime(field string) error {
	return &fieldError{field: field}
}

type fieldError struct{ field string }

func (e *fieldError) Error() string {
	return "body contains incorrect JSON type for field \"" + e.field + "\""
}

func NewEventHandlers(app *api.Application, event *service.EventService) *EventHandlers {
	return &EventHandlers{app: app, event: event}
}

func (h *EventHandlers) CreateVenue(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateVenueRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	venue, err := h.event.CreateVenue(r.Context(), middleware.UserID(r.Context()), &domain.Venue{
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
	shared.WriteJSON(w, http.StatusCreated, toVenueDTO(venue))
}

func (h *EventHandlers) ListVenues(w http.ResponseWriter, r *http.Request) {
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	result, err := h.event.ListVenues(r.Context(), page, limit)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.VenueDTO, 0, len(result.Items))
	for _, v := range result.Items {
		out = append(out, toVenueDTO(v))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   result.Total,
		HasNext: page*limit < result.Total,
	})
}

func (h *EventHandlers) ListCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := h.event.ListCategories(r.Context())
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.CategoryDTO, 0, len(cats))
	for _, c := range cats {
		out = append(out, dto.CategoryDTO{ID: c.ID, Slug: c.Slug, Name: c.Name})
	}
	shared.WriteJSON(w, http.StatusOK, out)
}

func (h *EventHandlers) CreateEvent(w http.ResponseWriter, r *http.Request) {
	req, err := parseEventRequest(w, r)
	if err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	event, err := h.event.CreateEvent(r.Context(), middleware.UserID(r.Context()), req)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, toEventDTO(event))
}

func (h *EventHandlers) UpdateEvent(w http.ResponseWriter, r *http.Request) {
	req, err := parseEventRequest(w, r)
	if err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	event, err := h.event.UpdateEvent(r.Context(), middleware.UserID(r.Context()), r.PathValue("id"), req)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, toEventDTO(event))
}

func (h *EventHandlers) Publish(w http.ResponseWriter, r *http.Request) {
	event, err := h.event.Publish(r.Context(), middleware.UserID(r.Context()), r.PathValue("id"))
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, toEventDTO(event))
}

func (h *EventHandlers) GetEvent(w http.ResponseWriter, r *http.Request) {
	event, err := h.event.GetEvent(r.Context(), r.PathValue("id"))
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, toEventDTO(event))
}

func (h *EventHandlers) ListEvents(w http.ResponseWriter, r *http.Request) {
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	result, err := h.event.ListEvents(r.Context(), page, limit)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.EventDTO, 0, len(result.Items))
	for _, e := range result.Items {
		out = append(out, toEventDTO(e))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   result.Total,
		HasNext: page*limit < result.Total,
	})
}

func parseEventRequest(w http.ResponseWriter, r *http.Request) (*domain.Event, error) {
	var req dto.CreateEventRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		return nil, err
	}

	starts, err := time.Parse(time.RFC3339, req.StartsAt)
	if err != nil {
		return nil, errBadStartsAt
	}
	e := &domain.Event{
		VenueID:      req.VenueID,
		CategoryID:   req.CategoryID,
		Title:        req.Title,
		Description:  req.Description,
		StartsAt:     starts,
		PriceIsFree:  req.PriceIsFree,
		PriceDisplay: req.PriceDisplay,
		ActionType:   req.ActionType,
		ActionTarget: req.ActionTarget,
		MaxAttendees: req.MaxAttendees,
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

func toVenueDTO(v *domain.Venue) dto.VenueDTO {
	return dto.VenueDTO{
		ID:          v.ID,
		Name:        v.Name,
		Address:     v.Address,
		Latitude:    v.Latitude,
		Longitude:   v.Longitude,
		City:        v.City,
		CountryCode: v.CountryCode,
		Status:      v.Status,
	}
}

func toEventDTO(e *domain.Event) dto.EventDTO {
	return dto.EventDTO{
		ID:               e.ID,
		OrganizerID:      e.OrganizerID,
		VenueID:          e.VenueID,
		CategoryID:       e.CategoryID,
		Title:            e.Title,
		Description:      e.Description,
		StartsAt:         e.StartsAt,
		EndsAt:           e.EndsAt,
		PriceIsFree:      e.PriceIsFree,
		PriceDisplay:     e.PriceDisplay,
		ActionType:       e.ActionType,
		Status:           e.Status,
		ModerationStatus: e.ModerationStatus,
		MaxAttendees:     e.MaxAttendees,
		CreatedAt:        e.CreatedAt,
	}
}

const (
	defaultPage  = 1
	defaultLimit = 20
	maxLimit     = 100
)

// parsePagination reads page/limit query params, validates them, and writes a
// 400 if invalid. Returns page, limit, and whether parsing succeeded.
func parsePagination(w http.ResponseWriter, r *http.Request) (page, limit int, ok bool) {
	page = defaultPage
	limit = defaultLimit

	if v := r.URL.Query().Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			shared.WriteErrorJSON(w, http.StatusBadRequest, "bad_request", "page must be a positive integer")
			return 0, 0, false
		}
		page = n
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxLimit {
			shared.WriteErrorJSON(w, http.StatusBadRequest, "bad_request", "limit must be between 1 and 100")
			return 0, 0, false
		}
		limit = n
	}
	return page, limit, true
}

// writePaginated writes a {"data": [...], "pagination": {...}} response.
func writePaginated[T any](w http.ResponseWriter, status int, data []T, meta dto.PaginationMeta) {
	resp := dto.PaginatedResponse[T]{Data: data, Pagination: meta}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}
