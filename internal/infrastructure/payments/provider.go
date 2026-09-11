// Package payments abstracts the ticket-checkout payment provider. The API
// talks to a Provider (Chapa in production, noop in local dev/tests); the
// authoritative money state is written by the provider's webhook handler.
package payments

import "context"

// Normalized provider statuses returned by Verify. Provider-specific values
// ("success", "failed", "pending", "authorized", ...) are mapped onto these.
const (
	StatusSuccess = "success"
	StatusFailed  = "failed"
	StatusPending = "pending"
)

// InitializeRequest describes a checkout to open with the provider. TxRef is
// the unique reference we control (the order id); AmountMinor is in the
// currency's minor unit (e.g. cents) and is converted to the provider's major
// unit by the implementation.
type InitializeRequest struct {
	TxRef       string
	AmountMinor int64
	Currency    string
	Email       string
	FirstName   string
	LastName    string
	CallbackURL string
	ReturnURL   string
	Description string
}

// InitializeResult is the provider's response to opening a checkout.
type InitializeResult struct {
	ProviderRef string
	RedirectURL string
}

// VerifyResult is the provider's authoritative view of a transaction.
type VerifyResult struct {
	Status      string
	AmountMinor int64
	Currency    string
}

// Provider is a payment gateway integration.
type Provider interface {
	// Name is the stable provider key persisted on the payment ledger.
	Name() string
	// Initialize opens a checkout and returns where to send the buyer.
	Initialize(ctx context.Context, req InitializeRequest) (InitializeResult, error)
	// Verify re-queries the provider for the authoritative transaction status.
	Verify(ctx context.Context, providerRef string) (VerifyResult, error)
}
