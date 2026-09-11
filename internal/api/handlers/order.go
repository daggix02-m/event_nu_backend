package handlers

import (
	"net/http"
	"strings"

	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
	"github.com/daggix02-m/event_nu/backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/repository"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// CreateOrder (POST /events/{id}/orders, idempotent) atomically reserves
// inventory, creates the pending order, and — when a payment provider is
// configured — starts a checkout and returns where to redirect the buyer.
func (h *OrderHandlers) CreateOrder(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	var req dto.OrderCreateRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if len(req.Items) == 0 {
		h.app.ClientError(w, r, http.StatusUnprocessableEntity, "validation_error", "An order must contain at least one ticket.")
		return
	}
	items := make([]repository.OrderItemInput, 0, len(req.Items))
	for _, it := range req.Items {
		items = append(items, repository.OrderItemInput{
			TicketTypeID: strings.TrimSpace(it.TicketTypeID),
			Quantity:     it.Quantity,
		})
	}
	order, err := h.order.CreateOrder(r.Context(), middleware.UserID(r.Context()), eventID, items)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}

	out := toOrderDTO(order)
	if h.pay != nil {
		init, err := h.pay.Initiate(r.Context(), order)
		if err != nil {
			// A failed checkout rolls back the whole request transaction — the
			// pending order and its reservations vanish with it.
			h.app.AppError(w, r, err)
			return
		}
		if init != nil {
			out.Payment = &dto.PaymentInitDTO{
				Provider:    init.Provider,
				ProviderRef: init.ProviderRef,
				RedirectURL: init.RedirectURL,
			}
		}
	}
	shared.WriteJSON(w, http.StatusCreated, out)
}

// GetOrder (GET /orders/{id}) — the buyer or the event's organizer.
func (h *OrderHandlers) GetOrder(w http.ResponseWriter, r *http.Request) {
	orderID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	order, err := h.order.GetOrder(r.Context(), orderID)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, toOrderDTO(order))
}

// MyOrders (GET /me/orders) — the caller's orders, newest first.
func (h *OrderHandlers) MyOrders(w http.ResponseWriter, r *http.Request) {
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	result, err := h.order.MyOrders(r.Context(), middleware.UserID(r.Context()), page, limit)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.OrderDTO, 0, len(result.Items))
	for _, o := range result.Items {
		out = append(out, toOrderDTO(o))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   result.Total,
		HasNext: page*limit < result.Total,
	})
}

// CancelOrder (POST /orders/{id}/cancel) — the buyer cancels their own pending
// order, releasing reserved inventory.
func (h *OrderHandlers) CancelOrder(w http.ResponseWriter, r *http.Request) {
	orderID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	order, err := h.order.CancelOrder(r.Context(), middleware.UserID(r.Context()), orderID)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, toOrderDTO(order))
}

func toOrderDTO(o *domain.Order) dto.OrderDTO {
	out := dto.OrderDTO{
		ID:              o.ID,
		EventID:         o.EventID,
		Status:          o.Status,
		Currency:        o.Currency,
		SubtotalMinor:   o.SubtotalMinor,
		TotalMinor:      o.TotalMinor,
		CreatedAt:       o.CreatedAt,
		UpdatedAt:       o.UpdatedAt,
		PaidAt:          o.PaidAt,
		CancelledAt:     o.CancelledAt,
		PaymentProvider: o.PaymentProvider,
		ProviderRef:     o.ProviderRef,
		Items:           make([]dto.OrderItemDTO, 0, len(o.Items)),
	}
	for _, it := range o.Items {
		out.Items = append(out.Items, dto.OrderItemDTO{
			ID:           it.ID,
			TicketTypeID: it.TicketTypeID,
			TicketType:   it.TicketType,
			Quantity:     it.Quantity,
			Currency:     it.Currency,
			UnitPrice:    it.UnitPrice,
			Subtotal:     it.Subtotal,
		})
	}
	return out
}
