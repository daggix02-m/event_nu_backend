package domain

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	StatusSuccess = "success"
	StatusFailed  = "failed"
	StatusPending = "pending"
)

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

type InitializeResult struct {
	ProviderRef string
	RedirectURL string
}

type VerifyResult struct {
	Status      string
	AmountMinor int64
	Currency    string
}

type PaymentProvider interface {
	Name() string

	Initialize(ctx context.Context, req InitializeRequest) (InitializeResult, error)

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
