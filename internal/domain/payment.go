package domain

import "time"

// Payment statuses mirror the payment_status enum in migration 00017.
const (
	PaymentStatusPending    = "pending"
	PaymentStatusAuthorized = "authorized"
	PaymentStatusPaid       = "paid"
	PaymentStatusFailed     = "failed"
	PaymentStatusRefunded   = "refunded"
	PaymentStatusCancelled  = "cancelled"
)

// Payment is one row of the provider payment ledger. The authoritative money
// state lives here (not on the order); provider_payment_id is the tx_ref we
// generated at card-init (the order id) and is what the webhook keys on.
type Payment struct {
	ID                string
	OrderID           string
	Provider          string
	ProviderPaymentID string
	Status            string
	AmountMinor       int64
	Currency          string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	PaidAt            *time.Time
	FailedAt          *time.Time
}

// PaymentInit is the client-facing result of starting a provider checkout.
type PaymentInit struct {
	Provider    string
	ProviderRef string
	RedirectURL string
}
