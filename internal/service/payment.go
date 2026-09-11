package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/payments"
	"github.com/daggix02-m/event_nu/backend/internal/repository"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// paymentOrderStore is the order surface PaymentService needs.
type paymentOrderStore interface {
	GetOrderByID(ctx context.Context, orderID string) (*domain.Order, error)
	ConfirmPaid(ctx context.Context, orderID, provider, providerRef string) (*domain.Order, []*domain.Ticket, error)
	FailPayment(ctx context.Context, orderID, provider, providerRef string) error
}

// paymentLedgerStore is the payments-ledger surface PaymentService needs.
type paymentLedgerStore interface {
	RecordPending(ctx context.Context, p *domain.Payment) error
	UpsertFromWebhook(ctx context.Context, p *domain.Payment, rawBody []byte) error
}

type paymentUserStore interface {
	GetByID(ctx context.Context, id string) (*domain.User, error)
}

// PaymentService starts provider checkouts and applies provider webhook
// outcomes to orders + the payments ledger.
type PaymentService struct {
	orders        paymentOrderStore
	payments      paymentLedgerStore
	users         paymentUserStore
	provider      payments.Provider
	notify        Notifier
	callbackURL   string // the provider POSTs outcomes here (this server's webhook)
	returnURL     string // the buyer lands here after the provider checkout
	webhookSecret string // verifies the provider's signature headers
}

func NewPaymentService(orders paymentOrderStore, payments paymentLedgerStore, users paymentUserStore,
	provider payments.Provider, notify Notifier, cfg config.Config) *PaymentService {
	return &PaymentService{
		orders:        orders,
		payments:      payments,
		users:         users,
		provider:      provider,
		notify:        notify,
		callbackURL:   cfg.APIPublicBase + "/webhooks/payments/" + provider.Name(),
		returnURL:     cfg.ChapaRedirectBase,
		webhookSecret: cfg.ChapaWebhookSecret,
	}
}

// ProviderName exposes the active provider key (used by handlers that don't want
// to reach into service internals).
func (s *PaymentService) ProviderName() string { return s.provider.Name() }

// Initiate opens a provider checkout for a fresh pending order and opens the
// payment-ledger row. Returns (nil, nil) when no provider is configured
// (PAYMENT_PROVIDER=noop in local dev/tests): the order is returned without a
// redirect and the buyer can cancel it. On provider failure an AppError (502)
// is returned; the caller's request transaction — which also created the order
// and its reservations — is rolled back, so nothing leaks.
func (s *PaymentService) Initiate(ctx context.Context, order *domain.Order) (*domain.PaymentInit, error) {
	if s.provider.Name() == "noop" {
		return nil, nil
	}

	var email string
	if u, err := s.users.GetByID(ctx, order.UserID); err == nil {
		email = u.Email
	}

	init, err := s.provider.Initialize(ctx, payments.InitializeRequest{
		TxRef:       order.ID,
		AmountMinor: order.TotalMinor,
		Currency:    order.Currency,
		Email:       email,
		CallbackURL: s.callbackURL,
		ReturnURL:   s.returnURL,
		Description: "Order " + order.ID,
	})
	if err != nil {
		return nil, shared.NewAppError("payment_unavailable", "Could not start payment. Please try again.", http.StatusBadGateway)
	}

	if err := s.payments.RecordPending(ctx, &domain.Payment{
		OrderID:           order.ID,
		Provider:          s.provider.Name(),
		ProviderPaymentID: order.ID,
		Status:            domain.PaymentStatusPending,
		AmountMinor:       order.TotalMinor,
		Currency:          order.Currency,
	}); err != nil {
		return nil, err
	}

	// ProviderRef is the single stable key every component keys on: the order
	// id = tx_ref = the ledger's provider_payment_id = orders.provider_ref.
	return &domain.PaymentInit{
		Provider:    s.provider.Name(),
		ProviderRef: order.ID,
		RedirectURL: init.RedirectURL,
	}, nil
}

// WebhookResult summarises how a provider webhook event was handled.
type WebhookResult struct {
	OrderID string
	Status  string // "paid" | "failed" | "pending"
}

// HandleWebhook processes a provider webhook event. The raw body is required so
// the signature can be verified over exactly what the provider signed. Returns
// AppErrors for client problems (bad signature 401, malformed payload 400,
// unknown order 404) and raw errors for provider-verify failures (the provider
// will retry them). Runs inside the webhook request transaction (privileged
// 'service' role) so the order state change commits atomically.
func (s *PaymentService) HandleWebhook(ctx context.Context, rawBody []byte, signatures ...string) (WebhookResult, error) {
	if !payments.SignatureValid(s.webhookSecret, rawBody, signatures...) {
		return WebhookResult{}, shared.NewAppError("invalid_signature", "Webhook signature verification failed.", http.StatusUnauthorized)
	}

	// Authority comes from re-querying the provider (Chapa: "always re-query
	// our API to verify"), never from trusting the event body's own status.
	txRef, ok := parseWebhookTxRef(rawBody)
	if !ok {
		return WebhookResult{}, shared.NewAppError("invalid_payload", "Webhook payload is missing the transaction reference.", http.StatusBadRequest)
	}

	verify, err := s.provider.Verify(ctx, txRef)
	if err != nil {
		return WebhookResult{}, err
	}

	switch verify.Status {
	case payments.StatusSuccess:
		return s.applySuccess(ctx, txRef, rawBody)
	case payments.StatusFailed:
		return s.applyFailure(ctx, txRef, verify, rawBody)
	default:
		// pending/authorized — no state change yet; await a later event.
		return WebhookResult{OrderID: txRef, Status: "pending"}, nil
	}
}

func (s *PaymentService) applySuccess(ctx context.Context, txRef string, rawBody []byte) (WebhookResult, error) {
	order, _, err := s.orders.ConfirmPaid(ctx, txRef, s.provider.Name(), txRef)
	if err != nil {
		if errors.Is(err, repository.ErrOrderAlreadyPaid) {
			return WebhookResult{OrderID: txRef, Status: "paid"}, nil // duplicate webhook — idempotent no-op
		}
		if errors.Is(err, shared.ErrNotFound) {
			return WebhookResult{}, shared.NewAppError("order_not_found", "No order matches this transaction.", http.StatusNotFound)
		}
		return WebhookResult{}, err
	}

	if payErr := s.payments.UpsertFromWebhook(ctx, &domain.Payment{
		OrderID:           order.ID,
		Provider:          s.provider.Name(),
		ProviderPaymentID: txRef,
		Status:            domain.PaymentStatusPaid,
		AmountMinor:       order.TotalMinor,
		Currency:          order.Currency,
	}, rawBody); payErr != nil {
		return WebhookResult{}, payErr
	}

	// Fan-out is best-effort: order confirmation must never fail the webhook.
	// notify_user validates p_type against the notification_type enum, so we
	// reuse the existing 'ticket' value rather than inventing a new one.
	if err := s.notify.NotifyUser(ctx, order.UserID, "ticket", "Payment confirmed",
		"Your payment was confirmed. Your tickets are ready in your wallet."); err != nil {
		// A failed notification must not roll back the payment confirmation;
		// without a logger there's nowhere to record it yet.
		_ = err
	}

	return WebhookResult{OrderID: order.ID, Status: "paid"}, nil
}

func (s *PaymentService) applyFailure(ctx context.Context, txRef string, verify payments.VerifyResult, rawBody []byte) (WebhookResult, error) {
	if err := s.orders.FailPayment(ctx, txRef, s.provider.Name(), txRef); err != nil {
		if errors.Is(err, repository.ErrOrderAlreadyPaid) {
			// The webhook lost a race with a success event: the payment is
			// already confirmed — a contradictory failure event is ignored.
			return WebhookResult{OrderID: txRef, Status: "paid"}, nil
		}
		if errors.Is(err, shared.ErrNotFound) {
			return WebhookResult{}, shared.NewAppError("order_not_found", "No order matches this transaction.", http.StatusNotFound)
		}
		return WebhookResult{}, err
	}

	// The failed path reached here only for a pending order; its row carries the
	// authoritative amount/currency for the ledger (the trigger forbids a
	// currency mismatch). Fall back to provider verify output when the order is
	// unavailable.
	amount := verify.AmountMinor
	currency := verify.Currency
	if order, oerr := s.orders.GetOrderByID(ctx, txRef); oerr == nil {
		if amount <= 0 {
			amount = order.TotalMinor
		}
		if currency == "" {
			currency = order.Currency
		}
	} else if currency == "" {
		// Cannot satisfy the currency trigger — leave the ledger without a row
		// rather than write garbage. The order itself is already failed.
		return WebhookResult{OrderID: txRef, Status: "failed"}, nil
	}

	if payErr := s.payments.UpsertFromWebhook(ctx, &domain.Payment{
		OrderID:           txRef,
		Provider:          s.provider.Name(),
		ProviderPaymentID: txRef,
		Status:            domain.PaymentStatusFailed,
		AmountMinor:       amount,
		Currency:          currency,
	}, rawBody); payErr != nil {
		return WebhookResult{}, payErr
	}

	return WebhookResult{OrderID: txRef, Status: "failed"}, nil
}

// parseWebhookTxRef extracts the transaction reference from a Chapa webhook
// payload. Chapa names it "tx_ref" in the event body; common aliases in the
// wild are accepted too.
func parseWebhookTxRef(raw []byte) (string, bool) {
	var body struct {
		TxRef  string `json:"tx_ref"`
		Txref  string `json:"txref"`
		TrxRef string `json:"trx_ref"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", false
	}
	for _, s := range []string{body.TxRef, body.Txref, body.TrxRef} {
		if s != "" {
			return s, true
		}
	}
	return "", false
}
