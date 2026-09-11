package payments

import (
	"context"
	"fmt"
)

// NoopProvider is the default provider when no gateway is configured
// (PAYMENT_PROVIDER=noop, the local dev/test posture). Initialize returns a
// fake checkout URL so clients can exercise the flow; Verify always reports
// pending so an unconfigured webhook can never change state.
type NoopProvider struct{}

func (p *NoopProvider) Name() string { return "noop" }

func (p *NoopProvider) Initialize(_ context.Context, req InitializeRequest) (InitializeResult, error) {
	return InitializeResult{
		ProviderRef: req.TxRef,
		RedirectURL: fmt.Sprintf("https://checkout.noop.example/%s", req.TxRef),
	}, nil
}

func (p *NoopProvider) Verify(context.Context, string) (VerifyResult, error) {
	return VerifyResult{Status: StatusPending}, nil
}

var _ Provider = (*NoopProvider)(nil)
