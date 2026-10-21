package payments

import (
	"context"
	"fmt"

	"github.com/daggix02-m/event_nu_backend/internal/domain"
)

// NoopProvider is the default provider when no gateway is configured
// (PAYMENT_PROVIDER=noop, the local dev/test posture). Initialize returns a
// fake checkout URL so clients can exercise the flow; Verify always reports
// pending so an unconfigured webhook can never change state.
type NoopProvider struct{}

func (p *NoopProvider) Name() string { return "noop" }

func (p *NoopProvider) Initialize(_ context.Context, req domain.InitializeRequest) (domain.InitializeResult, error) {
	return domain.InitializeResult{
		ProviderRef: req.TxRef,
		RedirectURL: fmt.Sprintf("https://checkout.noop.example/%s", req.TxRef),
	}, nil
}

func (p *NoopProvider) Verify(context.Context, string) (domain.VerifyResult, error) {
	return domain.VerifyResult{Status: domain.StatusPending}, nil
}

var _ domain.PaymentProvider = (*NoopProvider)(nil)
