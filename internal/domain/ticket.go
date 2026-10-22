package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

type TicketType struct {
	ID            string
	EventID       string
	Name          string
	Description   *string
	PriceMinor    int64
	Currency      string
	QuantityTotal *int
	QuantitySold  int
	SalesStart    *time.Time
	SalesEnd      *time.Time
	IsActive      bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type TicketTypeWithAvailability struct {
	TicketType
	SoldOut   bool
	SalesOpen bool
}

// Ticket is one issued admission encoded in a QR payload. code_hash is the
// sha256 of the canonical QR payload string: it uniquely fingerprints the
// signed credential so check-in can look it up without trusting the id.
type Ticket struct {
	ID           string
	OrderItemID  string
	UserID       string
	EventID      string
	TicketTypeID string
	CodeHash     string
	Status       string
	IssuedAt     time.Time
	UsedAt       *time.Time
	CreatedAt    time.Time
}

// TicketQRPayload is exactly what the check-in device scans: the three
// identifying fields plus an HMAC-SHA256 over their canonical concatenation.
// The full protocol spec is maintained internally by the maintainers.
type TicketQRPayload struct {
	TicketID string `json:"ticket_id"`
	EventID  string `json:"event_id"`
	IssuedAt string `json:"issued_at"`
	HMAC     string `json:"hmac"` // hex HMAC-SHA256
}

type TicketWithMeta struct {
	Ticket
	TicketTypeName string
	EventTitle     string
}

type TicketWithQR struct {
	Ticket
	Payload TicketQRPayload
}

// TicketPayloadCanonical is the single canonical string every QR credential is
// signed over: ticket_id | event_id | RFC3339 issuance time. Both the HMAC and
// the stored code_hash derive from it, so a payload's hmac is only valid for
// exactly one ticket/event/issuance binding.
func TicketPayloadCanonical(ticketID, eventID string, issuedAt time.Time) string {
	return fmt.Sprintf("%s|%s|%s", ticketID, eventID, issuedAt.UTC().Format(time.RFC3339))
}

// TicketPayloadCodeHash is the sha256 of the canonical payload string. It is
// stored UNIQUELY on the tickets row and is how check-in finds the ticket.
// No server secret is involved — the secret's job is the HMAC below.
func TicketPayloadCodeHash(ticketID, eventID string, issuedAt time.Time) string {
	sum := sha256.Sum256([]byte(TicketPayloadCanonical(ticketID, eventID, issuedAt)))
	return hex.EncodeToString(sum[:])
}

// SignTicketPayload returns the hex HMAC-SHA256 of the canonical payload under
// the given server secret.
func SignTicketPayload(ticketID, eventID string, issuedAt time.Time, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(TicketPayloadCanonical(ticketID, eventID, issuedAt)))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyTicketPayload constants-times a submitted HMAC against the expected
// value for the given binding. Returns false on any mismatch (tamper).
func VerifyTicketPayload(ticketID, eventID string, issuedAt time.Time, secret, wantHMAC string) bool {
	got := SignTicketPayload(ticketID, eventID, issuedAt, secret)
	return hmac.Equal([]byte(got), []byte(wantHMAC))
}
