package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/repository"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/daggix02-m/event_nu/backend/internal/validator"
)

// orderStore is the order-repository surface OrderService needs.
type orderStore interface {
	CreateOrder(ctx context.Context, userID, eventID string, inputs []repository.OrderItemInput) (*domain.Order, error)
	GetOrderByID(ctx context.Context, orderID string) (*domain.Order, error)
	ListByUser(ctx context.Context, userID string, limit, offset int) ([]*domain.Order, error)
	CountByUser(ctx context.Context, userID string) (int, error)
	Cancel(ctx context.Context, userID, orderID string) error
	ConfirmPaid(ctx context.Context, orderID, provider, providerRef string) (*domain.Order, []*domain.Ticket, error)
	ListTicketsByUser(ctx context.Context, userID string) ([]*domain.Ticket, error)
	CheckIn(ctx context.Context, codeHash, eventID string) (string, error)
	GetTicketByID(ctx context.Context, ticketID string) (*domain.TicketWithMeta, error)
}

// ticketTypeStore is the ticket-types repository surface OrderService needs.
type ticketTypeStore interface {
	Create(ctx context.Context, tt *domain.TicketType) (*domain.TicketType, error)
	GetByID(ctx context.Context, id string) (*domain.TicketType, error)
	Update(ctx context.Context, tt *domain.TicketType) (*domain.TicketType, error)
	ListByEvent(ctx context.Context, eventID string) ([]*domain.TicketType, error)
	ListByEventPublic(ctx context.Context, eventID string) ([]*domain.TicketType, error)
}

type OrderService struct {
	orders   orderStore
	types    ticketTypeStore
	events   eventStore
	orgs     organizerStore
	qrSecret string
}

func NewOrderService(orders orderStore, types ticketTypeStore, events eventStore, orgs organizerStore, qrSecret string) *OrderService {
	return &OrderService{orders: orders, types: types, events: events, orgs: orgs, qrSecret: qrSecret}
}

// requireEventOrganizer resolves the caller's active organizer and verifies it
// owns the event (403 otherwise), returning the event for good measure.
func (s *OrderService) requireEventOrganizer(ctx context.Context, userID, eventID string) (*domain.Event, error) {
	org, err := s.orgs.GetOrganizerByOwner(ctx, userID)
	if err != nil {
		if err == shared.ErrNotFound {
			return nil, shared.NewAppError("organizer_required", "You need an approved organizer account.", http.StatusForbidden)
		}
		return nil, err
	}
	if org.Status != "active" {
		return nil, shared.NewAppError("organizer_required", "Your organizer account is not active.", http.StatusForbidden)
	}
	ev, err := s.events.GetByID(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if ev.OrganizerID != org.ID {
		return nil, shared.NewAppError("forbidden", "You can only manage your own events.", http.StatusForbidden)
	}
	return ev, nil
}

// ---- ticket types ------------------------------------------------------------

// CreateTicketType adds a purchasable tier to an organizer-owned event.
func (s *OrderService) CreateTicketType(ctx context.Context, userID string, tt *domain.TicketType) (*domain.TicketType, error) {
	if _, err := s.requireEventOrganizer(ctx, userID, tt.EventID); err != nil {
		return nil, err
	}
	if err := validateTicketType(tt); err != nil {
		return nil, err
	}
	tt.Name = strings.TrimSpace(tt.Name)
	tt.Currency = strings.ToUpper(strings.TrimSpace(tt.Currency))
	return s.types.Create(ctx, tt)
}

// ListTicketTypes returns every tier of an organizer-owned event, decorated
// with sold-out and sales-window availability.
func (s *OrderService) ListTicketTypes(ctx context.Context, userID, eventID string) ([]*domain.TicketTypeWithAvailability, error) {
	if _, err := s.requireEventOrganizer(ctx, userID, eventID); err != nil {
		return nil, err
	}
	tiers, err := s.types.ListByEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	return decorateAvailability(tiers, time.Now()), nil
}

// ListTicketTypesPublic returns the active tiers of a publicly visible event,
// each decorated with sold-out and sales-window availability.
func (s *OrderService) ListTicketTypesPublic(ctx context.Context, eventID string) ([]*domain.TicketTypeWithAvailability, error) {
	if _, err := s.events.GetByID(ctx, eventID); err != nil {
		return nil, err
	}
	tiers, err := s.types.ListByEventPublic(ctx, eventID)
	if err != nil {
		return nil, err
	}
	return decorateAvailability(tiers, time.Now()), nil
}

func decorateAvailability(tiers []*domain.TicketType, now time.Time) []*domain.TicketTypeWithAvailability {
	out := make([]*domain.TicketTypeWithAvailability, 0, len(tiers))
	for _, tt := range tiers {
		soldOut := tt.QuantityTotal != nil && tt.QuantitySold >= *tt.QuantityTotal
		open := (tt.SalesStart == nil || !now.Before(*tt.SalesStart)) &&
			(tt.SalesEnd == nil || !now.After(*tt.SalesEnd))
		out = append(out, &domain.TicketTypeWithAvailability{TicketType: *tt, SoldOut: soldOut, SalesOpen: open})
	}
	return out
}

// UpdateTicketType applies an organizer's edits to a tier (price snapshot rules
// mean quantity_sold/event_id can never change here).
func (s *OrderService) UpdateTicketType(ctx context.Context, userID, eventID, ticketTypeID string, up *domain.TicketType) (*domain.TicketType, error) {
	if _, err := s.requireEventOrganizer(ctx, userID, eventID); err != nil {
		return nil, err
	}
	existing, err := s.types.GetByID(ctx, ticketTypeID)
	if err != nil {
		return nil, err
	}
	if existing.EventID != eventID {
		return nil, shared.NewAppError("forbidden", "You can only manage your own events.", http.StatusForbidden)
	}
	up.ID = ticketTypeID
	up.EventID = eventID
	if err := validateTicketType(up); err != nil {
		return nil, err
	}
	up.Name = strings.TrimSpace(up.Name)
	up.Currency = existing.Currency
	return s.types.Update(ctx, up)
}

// ---- orders ------------------------------------------------------------------

// CreateOrder validates a purchasable event, then atomically reserves inventory
// and creates the pending order. Unavailable lines abort the whole order.
func (s *OrderService) CreateOrder(ctx context.Context, userID, eventID string, items []repository.OrderItemInput) (*domain.Order, error) {
	if len(items) == 0 {
		return nil, shared.NewAppError("validation_error", "An order must contain at least one ticket.", http.StatusUnprocessableEntity)
	}
	for _, it := range items {
		if it.Quantity < 1 {
			return nil, shared.NewAppError("validation_error", "Ticket quantity must be at least one.", http.StatusUnprocessableEntity)
		}
	}
	if _, err := s.events.GetByID(ctx, eventID); err != nil {
		return nil, err
	}
	order, err := s.orders.CreateOrder(ctx, userID, eventID, items)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrTicketSoldOut):
			return nil, shared.NewAppError("ticket_sold_out", "One or more ticket types have sold out.", http.StatusConflict)
		case errors.Is(err, repository.ErrTicketSalesClosed):
			return nil, shared.NewAppError("sales_closed", "One or more ticket types are not on sale.", http.StatusConflict)
		case errors.Is(err, repository.ErrOrderItemInvalid):
			return nil, shared.NewAppError("invalid_items", "One or more ticket types are not available for this event.", http.StatusUnprocessableEntity)
		default:
			return nil, err
		}
	}
	return order, nil
}

// GetOrder returns an order the caller can see (owner or event organizer,
// enforced by the orders_select RLS policy).
func (s *OrderService) GetOrder(ctx context.Context, orderID string) (*domain.Order, error) {
	return s.orders.GetOrderByID(ctx, orderID)
}

func (s *OrderService) MyOrders(ctx context.Context, userID string, page, limit int) (PageResult[*domain.Order], error) {
	offset := (page - 1) * limit
	items, err := s.orders.ListByUser(ctx, userID, limit, offset)
	if err != nil {
		return PageResult[*domain.Order]{}, err
	}
	total, err := s.orders.CountByUser(ctx, userID)
	if err != nil {
		return PageResult[*domain.Order]{}, err
	}
	return PageResult[*domain.Order]{Items: items, Total: total}, nil
}

// CancelOrder cancels the buyer's own pending order and releases inventory.
func (s *OrderService) CancelOrder(ctx context.Context, userID, orderID string) (*domain.Order, error) {
	// Fetch first so a valid cancel can return the final order state and a
	// foreign/missing order 404s before any mutation.
	order, err := s.orders.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order.UserID != userID {
		return nil, shared.NewAppError("forbidden", "You can only cancel your own orders.", http.StatusForbidden)
	}
	if err := s.orders.Cancel(ctx, userID, orderID); err != nil {
		if errors.Is(err, repository.ErrOrderNotCancellable) {
			return nil, shared.NewAppError("order_not_cancellable", "Only pending orders can be cancelled.", http.StatusConflict)
		}
		return nil, err
	}
	order.Status = domain.OrderStatusCancelled
	return order, nil
}

// ConfirmPaid flips a pending order to paid and issues its tickets. Intended
// for the Phase 15 payment webhook (privileged context); exposed for wiring.
func (s *OrderService) ConfirmPaid(ctx context.Context, orderID, provider, providerRef string) (*domain.Order, []*domain.Ticket, error) {
	order, tickets, err := s.orders.ConfirmPaid(ctx, orderID, provider, providerRef)
	if err != nil {
		if errors.Is(err, repository.ErrOrderAlreadyPaid) {
			return nil, nil, shared.NewAppError("order_already_paid", "This order has already been confirmed.", http.StatusConflict)
		}
		return nil, nil, err
	}
	return order, tickets, nil
}

// ---- tickets / check-in ------------------------------------------------------

// MyTickets returns the caller's issued tickets with signed QR payloads.
func (s *OrderService) MyTickets(ctx context.Context, userID string) ([]*domain.TicketWithQR, error) {
	tickets, err := s.orders.ListTicketsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.TicketWithQR, 0, len(tickets))
	for _, t := range tickets {
		out = append(out, &domain.TicketWithQR{
			Ticket: *t,
			Payload: domain.TicketQRPayload{
				TicketID: t.ID,
				EventID:  t.EventID,
				IssuedAt: t.IssuedAt.UTC().Format(time.RFC3339),
				HMAC:     domain.SignTicketPayload(t.ID, t.EventID, t.IssuedAt, s.qrSecret),
			},
		})
	}
	return out, nil
}

// CheckIn validates an organizer's scan: the caller must own the event, the
// payload's HMAC must verify, and the ticket must be unused. Returns the ticket
// metadata on success so the organizer can see who/what was admitted.
func (s *OrderService) CheckIn(ctx context.Context, userID, eventID string, p *domain.TicketQRPayload) (*domain.TicketWithMeta, error) {
	if _, err := s.requireEventOrganizer(ctx, userID, eventID); err != nil {
		return nil, err
	}
	issuedAt, err := parseTicketIssuedAt(p.IssuedAt)
	if err != nil {
		return nil, invalidTicketCode()
	}
	if !domain.VerifyTicketPayload(p.TicketID, eventID, issuedAt, s.qrSecret, p.HMAC) {
		return nil, invalidTicketCode()
	}
	status, err := s.orders.CheckIn(ctx, domain.TicketPayloadCodeHash(p.TicketID, eventID, issuedAt), eventID)
	if err != nil {
		return nil, err
	}
	switch status {
	case "ok":
		return s.orders.GetTicketByID(ctx, p.TicketID)
	case "already_used":
		return nil, shared.NewAppError("already_checked_in", "This ticket has already been checked in.", http.StatusConflict)
	case "forbidden":
		return nil, shared.NewAppError("forbidden", "You can only check in tickets for your own events.", http.StatusForbidden)
	default: // not_found
		return nil, shared.NewAppError("invalid_code", "No matching ticket was found.", http.StatusNotFound)
	}
}

func parseTicketIssuedAt(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

func invalidTicketCode() error {
	return shared.NewAppError("invalid_code", "This QR code is invalid or has been tampered with.", http.StatusUnprocessableEntity)
}

func validateTicketType(tt *domain.TicketType) error {
	v := validator.New()
	v.Required(tt.Name, "name")
	v.MaxChars(tt.Name, 200, "name")
	if tt.Description != nil {
		v.MaxChars(*tt.Description, 2000, "description")
	}
	v.Check(tt.PriceMinor >= 0, "price_minor", "must be zero or greater")
	if tt.Currency != "" {
		v.Check(len(tt.Currency) == 3, "currency", "must be a 3-letter currency code")
	}
	if tt.QuantityTotal != nil {
		v.Check(*tt.QuantityTotal > 0, "quantity_total", "must be greater than zero")
	}
	if tt.SalesStart != nil && tt.SalesEnd != nil && tt.SalesEnd.Before(*tt.SalesStart) {
		v.AddError("sales_end", "must be on or after sales_start")
	}
	if !v.Valid() {
		return shared.NewAppError("validation_error", firstFieldError(v), http.StatusUnprocessableEntity)
	}
	return nil
}
