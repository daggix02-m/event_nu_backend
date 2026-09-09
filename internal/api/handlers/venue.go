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

// VenueHandlers serve the public venue detail endpoint and organizer venue
// updates (PATCH).
type VenueHandlers struct {
	app    *api.Application
	venues *service.VenueService
}

func NewVenueHandlers(app *api.Application, venues *service.VenueService) *VenueHandlers {
	return &VenueHandlers{app: app, venues: venues}
}

// Get returns a venue with its published events.
func (h *VenueHandlers) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	venue, events, err := h.venues.Detail(r.Context(), id, page, limit)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	eventOut := make([]dto.EventDTO, 0, len(events.Items))
	for _, e := range events.Items {
		eventOut = append(eventOut, toEventDTO(e))
	}
	shared.WriteJSON(w, http.StatusOK, dto.VenueDetailDTO{
		Venue:             toVenueDTO(venue),
		Events:            eventOut,
		EventsTotal:       events.Total,
		EventsPage:        page,
		EventsPageSize:    limit,
		EventsHasNextPage: page*limit < events.Total,
	})
}

// Update patches a venue owned by the caller's organizer.
func (h *VenueHandlers) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	var req dto.VenueUpdateRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	venue, err := h.venues.Update(r.Context(), middleware.UserID(r.Context()), id, &domain.Venue{
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
	shared.WriteJSON(w, http.StatusOK, toVenueDTO(venue))
}
