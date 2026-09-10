package dto

import "time"

// OrderCreateRequest is the order checkout payload (POST /events/{id}/orders).
type OrderCreateRequest struct {
	Items []OrderItemRequest `json:"items"`
}

type OrderItemRequest struct {
	TicketTypeID string `json:"ticket_type_id"`
	Quantity     int    `json:"quantity"`
}

// OrderDTO is the wire shape of an order; items are always included.
type OrderDTO struct {
	ID            string         `json:"id"`
	EventID       string         `json:"event_id"`
	Status        string         `json:"status"`
	Currency      string         `json:"currency"`
	SubtotalMinor int64          `json:"subtotal_minor"`
	TotalMinor    int64          `json:"total_minor"`
	Items         []OrderItemDTO `json:"items"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	PaidAt        *time.Time     `json:"paid_at"`
	CancelledAt   *time.Time     `json:"cancelled_at"`
}

type OrderItemDTO struct {
	ID           string `json:"id"`
	TicketTypeID string `json:"ticket_type_id"`
	TicketType   string `json:"ticket_type"`
	Quantity     int    `json:"quantity"`
	Currency     string `json:"currency"`
	UnitPrice    int64  `json:"unit_price"`
	Subtotal     int64  `json:"subtotal"`
}

// TicketDTO is an item of GET /me/tickets. QR carries the signed payload the
// client encodes as the check-in code.
type TicketDTO struct {
	ID           string      `json:"id"`
	OrderItemID  string      `json:"order_item_id"`
	EventID      string      `json:"event_id"`
	TicketTypeID string      `json:"ticket_type_id"`
	Status       string      `json:"status"`
	IssuedAt     time.Time   `json:"issued_at"`
	UsedAt       *time.Time  `json:"used_at"`
	QR           TicketQRDTO `json:"qr"`
}

// TicketQRDTO mirrors domain.TicketQRPayload for /me/tickets responses.
type TicketQRDTO struct {
	TicketID string `json:"ticket_id"`
	EventID  string `json:"event_id"`
	IssuedAt string `json:"issued_at"`
	HMAC     string `json:"hmac"`
}

// CheckInRequest is what a check-in device submits (POST /events/{id}/check-in):
// the decoded fields of the scanned QR payload.
type CheckInRequest struct {
	TicketID string `json:"ticket_id"`
	EventID  string `json:"event_id"`
	IssuedAt string `json:"issued_at"`
	HMAC     string `json:"hmac"`
}

// CheckInResponse reports a successful admission. Attendee PII is deliberately
// absent: users RLS does not expose other users' private profile rows.
type CheckInResponse struct {
	Status         string `json:"status"`
	TicketID       string `json:"ticket_id"`
	EventID        string `json:"event_id"`
	TicketTypeName string `json:"ticket_type_name"`
	EventTitle     string `json:"event_title"`
}
