package payments

import (
	"github.com/daggix02-m/event_nu_backend/internal/domain"
)

// Package payments hosts the payment-gateway adapters. The contracts
// (PaymentProvider, request/result shapes, normalized statuses, webhook-seal
// check) live in package domain (hexagonal slice 3); this package implements
// them (Chapa in production, noop in local dev/tests) and adapts each
// provider's HTTP protocol. The API talks to a Provider instance; the
// authoritative money state is written by the provider's webhook handler.
//
// New selects the provider implementation from its configured name. Anything
// other than "chapa" resolves to the no-op — safer than accidentally charging
// real money through a misconfigured gateway.
func New(name string, baseURL, secretKey string) domain.PaymentProvider {
	if name == chapaName {
		return NewChapa(baseURL, secretKey)
	}
	return &NoopProvider{}
}
