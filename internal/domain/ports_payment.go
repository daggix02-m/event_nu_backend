package domain

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Payment-gateway port (hexagonal slice 3, moved from the infrastructure
// package): the core describes CHECKOUTS, VERIFY, and webhook-seal semantics;
// chapa/noop adapters in package payments implement it. Type names keep their
// original shape so the move is mechanical.

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

// PaymentProvider is a payment gateway integration.
type PaymentProvider interface {
	// Name is the stable provider key persisted on the payment ledger.
	Name() string
	// Initialize opens a checkout and returns where to send the buyer.
	Initialize(ctx context.Context, req InitializeRequest) (InitializeResult, error)
	// Verify re-queries the provider for the authoritative transaction status.
	Verify(ctx context.Context, providerRef string) (VerifyResult, error)
}

// SignatureValid verifies a webhook signature: HMAC-SHA256 of the raw request
// body using the webhook secret, hex-encoded. The header may expose the
// signature under several keys; any single matching value suffices. The
// comparison is constant-time.
func SignatureValid(secret string, body []byte, provided ...string) bool {
	if secret == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)
	expectedHex := make([]byte, hex.EncodedLen(len(expected)))
	hex.Encode(expectedHex, expected)

	for _, p := range provided {
		if p == "" {
			continue
		}
		if hmac.Equal([]byte(strings.TrimSpace(strings.ToLower(p))), expectedHex) {
			return true
		}
	}
	return false
}
