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

type RsvpHandlers struct {
	app  *api.Application
	rsvp *service.RsvpService
}

func NewRsvpHandlers(app *api.Application, rsvp *service.RsvpService) *RsvpHandlers {
	return &RsvpHandlers{app: app, rsvp: rsvp}
}

func (h *RsvpHandlers) Create(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	public := true
	if r.ContentLength != 0 {
		var req dto.RsvpRequest
		if err := shared.DecodeJSON(w, r, &req); err != nil {
			h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if req.PublicRsvp != nil {
			public = *req.PublicRsvp
		}
	}
	rsvp, err := h.rsvp.Create(r.Context(), middleware.UserID(r.Context()), eventID, public)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	h.writeRsvpState(w, r, rsvp)
}

func (h *RsvpHandlers) Cancel(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if err := h.rsvp.Cancel(r.Context(), middleware.UserID(r.Context()), eventID); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, dto.RsvpState{Going: false})
}

func (h *RsvpHandlers) State(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	rsvp, err := h.rsvp.State(r.Context(), middleware.UserID(r.Context()), eventID)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	if rsvp == nil {
		shared.WriteJSON(w, http.StatusOK, dto.RsvpState{Going: false})
		return
	}
	h.writeRsvpState(w, r, rsvp)
}

func (h *RsvpHandlers) MyRsvps(w http.ResponseWriter, r *http.Request) {
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	result, err := h.rsvp.MyRsvps(r.Context(), middleware.UserID(r.Context()), page, limit)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.RsvpDTO, 0, len(result.Items))
	for _, item := range result.Items {
		out = append(out, toRsvpDTO(item))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   result.Total,
		HasNext: page*limit < result.Total,
	})
}

func (h *RsvpHandlers) writeRsvpState(w http.ResponseWriter, r *http.Request, rsvp *domain.Rsvp) {
	shared.WriteJSON(w, http.StatusOK, toRsvpState(rsvp))
}

func toRsvpState(rsvp *domain.Rsvp) dto.RsvpState {
	return dto.RsvpState{
		Going:   rsvp.Status == "confirmed" || rsvp.Status == "attended",
		Status:  rsvp.Status,
		Created: rsvp.CreatedAt,
	}
}

func toRsvpDTO(item *domain.RsvpWithEvent) dto.RsvpDTO {
	out := dto.RsvpDTO{
		EventID:    item.EventID,
		Status:     item.Status,
		PublicRSVP: item.PublicRsvp,
		CreatedAt:  item.CreatedAt,
	}
	if item.EventTime != nil {
		visible := item.EventTitle != ""
		out.Event = &dto.EventSummary{
			ID:        item.EventID,
			Title:     item.EventTitle,
			StartsAt:  *item.EventTime,
			Status:    "published",
			IsVisible: visible,
		}
	}
	return out
}
