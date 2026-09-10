package domain

import "time"

// Order statuses mirror the order_status enum in migration 00016.
const (
	OrderStatusPending   = "pending"
	OrderStatusPaid      = "paid"
	OrderStatusFailed    = "failed"
	OrderStatusCancelled = "cancelled"
	OrderStatusRefunded  = "refunded"
)

// Order is a purchase of ticket tiers against an event. Status starts
// 'pending'; the Phase 15 payment worker flips it to 'paid' (issuing tickets)
// or 'failed'. The buyer may cancel a pending order, which releases inventory.
type Order struct {
	ID              string
	UserID          string
	EventID         string
	Status          string
	Currency        string
	SubtotalMinor   int64
	TotalMinor      int64
	PaymentProvider *string
	ProviderRef     *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	PaidAt          *time.Time
	CancelledAt     *time.Time
	Items           []*OrderItem
}

// OrderItem is one line of an order: a quantity of a single ticket tier at the
// unit price captured at purchase time (prices are immutable snapshots).
type OrderItem struct {
	ID           string
	OrderID      string
	TicketTypeID string
	TicketType   string
	Quantity     int
	Currency     string
	UnitPrice    int64
	Subtotal     int64
	CreatedAt    time.Time
}
