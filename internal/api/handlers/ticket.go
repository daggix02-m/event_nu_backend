package handlers

import (
	"net/http"
	"strings"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
	"github.com/daggix02-m/event_nu/backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/service"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// OrderHandlers exposes ticket types, orders, the ticket wallet, and check-in.
type OrderHandlers struct {
	app   *api.Application
	order *service.OrderService
	pay   *service.PaymentService
}

func NewOrderHandlers(app *api.Application, order *service.OrderService, pay *service.PaymentService) *OrderHandlers {
	return &OrderHandlers{app: app, order: order, pay: pay}
}

// ---- ticket types ------------------------------------------------------------

// CreateTicketType (POST /events/{id}/ticket-types, organizer, idempotent).
func (h *OrderHandlers) CreateTicketType(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	var req dto.TicketTypeRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	currency := strings.ToUpper(strings.TrimSpace(req.Currency))
	if currency == "" {
		currency = "ETB"
	}
	tt, err := h.order.CreateTicketType(r.Context(), middleware.UserID(r.Context()), &domain.TicketType{
		EventID:       eventID,
		Name:          req.Name,
		Description:   req.Description,
		PriceMinor:    req.PriceMinor,
		Currency:      currency,
		QuantityTotal: req.QuantityTotal,
		SalesStart:    req.SalesStart,
		SalesEnd:      req.SalesEnd,
		IsActive:      true,
	})
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, toTicketTypeDTO(tt, false, false))
}

// ListTicketTypes (GET /events/{id}/ticket-types/manage, organizer).
func (h *OrderHandlers) ListTicketTypes(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	types, err := h.order.ListTicketTypes(r.Context(), middleware.UserID(r.Context()), eventID)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.TicketTypeDTO, 0, len(types))
	for _, tt := range types {
		out = append(out, toTicketTypeDTO(&tt.TicketType, tt.SoldOut, tt.SalesOpen))
	}
	shared.WriteJSON(w, http.StatusOK, out)
}

// ListTicketTypesPublic (GET /events/{id}/ticket-types, public).
func (h *OrderHandlers) ListTicketTypesPublic(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	types, err := h.order.ListTicketTypesPublic(r.Context(), eventID)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.TicketTypeDTO, 0, len(types))
	for _, tt := range types {
		out = append(out, toTicketTypeDTO(&tt.TicketType, tt.SoldOut, tt.SalesOpen))
	}
	shared.WriteJSON(w, http.StatusOK, out)
}

// UpdateTicketType (PATCH /events/{id}/ticket-types/{ticketTypeID}, organizer).
func (h *OrderHandlers) UpdateTicketType(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	ticketTypeID, ok := ParseUUIDParam(r, "ticketTypeID")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	var req dto.TicketTypeUpdate
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	tt, err := h.order.UpdateTicketType(r.Context(), middleware.UserID(r.Context()), eventID, ticketTypeID, &domain.TicketType{
		ID:            ticketTypeID,
		EventID:       eventID,
		Name:          req.Name,
		Description:   req.Description,
		PriceMinor:    req.PriceMinor,
		QuantityTotal: req.QuantityTotal,
		SalesStart:    req.SalesStart,
		SalesEnd:      req.SalesEnd,
		IsActive:      req.IsActive,
	})
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, toTicketTypeDTO(tt, false, false))
}

// ---- ticket wallet + check-in ------------------------------------------------

// MyTickets (GET /me/tickets) returns the caller's issued tickets with their
// signed QR payloads.
func (h *OrderHandlers) MyTickets(w http.ResponseWriter, r *http.Request) {
	tickets, err := h.order.MyTickets(r.Context(), middleware.UserID(r.Context()))
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.TicketDTO, 0, len(tickets))
	for _, tk := range tickets {
		out = append(out, dto.TicketDTO{
			ID:           tk.ID,
			OrderItemID:  tk.OrderItemID,
			EventID:      tk.EventID,
			TicketTypeID: tk.TicketTypeID,
			Status:       tk.Status,
			IssuedAt:     tk.IssuedAt,
			UsedAt:       tk.UsedAt,
			QR:           toTicketQRDTO(tk.Payload),
		})
	}
	shared.WriteJSON(w, http.StatusOK, out)
}

// CheckIn (POST /events/{id}/check-in, organizer) verifies the scanned QR
// payload and marks the ticket used.
func (h *OrderHandlers) CheckIn(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	var req dto.CheckInRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ticket, err := h.order.CheckIn(r.Context(), middleware.UserID(r.Context()), eventID, &domain.TicketQRPayload{
		TicketID: strings.TrimSpace(req.TicketID),
		EventID:  strings.TrimSpace(req.EventID),
		IssuedAt: strings.TrimSpace(req.IssuedAt),
		HMAC:     strings.TrimSpace(req.HMAC),
	})
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, dto.CheckInResponse{
		Status:         "ok",
		TicketID:       ticket.ID,
		EventID:        ticket.EventID,
		TicketTypeName: ticket.TicketTypeName,
		EventTitle:     ticket.EventTitle,
	})
}

func toTicketTypeDTO(tt *domain.TicketType, soldOut, salesOpen bool) dto.TicketTypeDTO {
	return dto.TicketTypeDTO{
		ID:            tt.ID,
		EventID:       tt.EventID,
		Name:          tt.Name,
		Description:   tt.Description,
		PriceMinor:    tt.PriceMinor,
		Currency:      tt.Currency,
		QuantityTotal: tt.QuantityTotal,
		QuantitySold:  tt.QuantitySold,
		SalesStart:    tt.SalesStart,
		SalesEnd:      tt.SalesEnd,
		IsActive:      tt.IsActive,
		SoldOut:       soldOut,
		SalesOpen:     salesOpen,
		CreatedAt:     tt.CreatedAt,
		UpdatedAt:     tt.UpdatedAt,
	}
}

func toTicketQRDTO(p domain.TicketQRPayload) dto.TicketQRDTO {
	return dto.TicketQRDTO{
		TicketID: p.TicketID,
		EventID:  p.EventID,
		IssuedAt: p.IssuedAt,
		HMAC:     p.HMAC,
	}
}
