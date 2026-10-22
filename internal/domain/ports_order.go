package domain

// Order persistence contracts (hexagonal slice 3). The order sentinels are
// domain invariants the SQL adapter returns and the core maps onto API
// outcomes; package repository raised them before, but the invariant names
// belong to the core, not the storage layer.

import "fmt"

var (
	ErrTicketSoldOut       = fmt.Errorf("ticket_type_sold_out")
	ErrTicketSalesClosed   = fmt.Errorf("ticket_type_sales_closed")
	ErrOrderItemInvalid    = fmt.Errorf("invalid_order_items")
	ErrOrderNotCancellable = fmt.Errorf("order_not_cancellable")
	ErrOrderAlreadyPaid    = fmt.Errorf("order_already_paid")
)

type OrderItemInput struct {
	TicketTypeID string
	Quantity     int
}
