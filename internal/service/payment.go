package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/daggix02-m/event_nu_backend/internal/config"
	"github.com/daggix02-m/event_nu_backend/internal/domain"
	"github.com/daggix02-m/event_nu_backend/internal/shared"
)

type paymentOrderStore interface {
	GetOrderByID(ctx context.Context, orderID string) (*domain.Order, error)
	ConfirmPaid(ctx context.Context, orderID, provider, providerRef string) (*domain.Order, []*domain.Ticket, error)
	FailPayment(ctx context.Context, orderID, provider, providerRef string) error
}

type paymentLedgerStore interface {
	RecordPending(ctx context.Context, p *domain.Payment) error
	UpsertFromWebhook(ctx context.Context, p *domain.Payment, rawBody []byte) error
}

type paymentUserStore interface {
	GetByID(ctx context.Context, id string) (*domain.User, error)
}

type PaymentService struct {
	orders        paymentOrderStore
	payments      paymentLedgerStore
	users         paymentUserStore
	provider      domain.PaymentProvider
	notify        Notifier
	callbackURL   string
	returnURL     string
	webhookSecret string
}

func NewPaymentService(orders paymentOrderStore, payments paymentLedgerStore, users paymentUserStore,
	provider domain.PaymentProvider, notify Notifier, cfg config.Config) *PaymentService {
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

func (s *PaymentService) ProviderName() string { return s.provider.Name() }

func (s *PaymentService) Initiate(ctx context.Context, order *domain.Order) (*domain.PaymentInit, error) {
	if s.provider.Name() == "noop" {
		return nil, nil
	}

	var email string
	if u, err := s.users.GetByID(ctx, order.UserID); err == nil {
		email = u.Email
	}

	init, err := s.provider.Initialize(ctx, domain.InitializeRequest{
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

	return &domain.PaymentInit{
		Provider:    s.provider.Name(),
		ProviderRef: order.ID,
		RedirectURL: init.RedirectURL,
	}, nil
}

type WebhookResult struct {
	OrderID string
	Status  string
}

func (s *PaymentService) HandleWebhook(ctx context.Context, rawBody []byte, signatures ...string) (WebhookResult, error) {
	if !domain.SignatureValid(s.webhookSecret, rawBody, signatures...) {
		return WebhookResult{}, shared.NewAppError("invalid_signature", "Webhook signature verification failed.", http.StatusUnauthorized)
	}

	txRef, ok := parseWebhookTxRef(rawBody)
	if !ok {
		return WebhookResult{}, shared.NewAppError("invalid_payload", "Webhook payload is missing the transaction reference.", http.StatusBadRequest)
	}

	verify, err := s.provider.Verify(ctx, txRef)
	if err != nil {
		return WebhookResult{}, err
	}

	switch verify.Status {
	case domain.StatusSuccess:
		return s.applySuccess(ctx, txRef, rawBody)
	case domain.StatusFailed:
		return s.applyFailure(ctx, txRef, verify, rawBody)
	default:

		return WebhookResult{OrderID: txRef, Status: "pending"}, nil
	}
}

func (s *PaymentService) applySuccess(ctx context.Context, txRef string, rawBody []byte) (WebhookResult, error) {
	order, _, err := s.orders.ConfirmPaid(ctx, txRef, s.provider.Name(), txRef)
	if err != nil {
		if errors.Is(err, domain.ErrOrderAlreadyPaid) {
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

	if err := s.notify.NotifyUser(ctx, order.UserID, "ticket", "Payment confirmed",
		"Your payment was confirmed. Your tickets are ready in your wallet."); err != nil {

		_ = err
	}

	return WebhookResult{OrderID: order.ID, Status: "paid"}, nil
}

func (s *PaymentService) applyFailure(ctx context.Context, txRef string, verify domain.VerifyResult, rawBody []byte) (WebhookResult, error) {
	if err := s.orders.FailPayment(ctx, txRef, s.provider.Name(), txRef); err != nil {
		if errors.Is(err, domain.ErrOrderAlreadyPaid) {
			// The webhook lost a race with a success event: the payment is
			// already confirmed — a contradictory failure event is ignored.
			return WebhookResult{OrderID: txRef, Status: "paid"}, nil
		}
		if errors.Is(err, shared.ErrNotFound) {
			return WebhookResult{}, shared.NewAppError("order_not_found", "No order matches this transaction.", http.StatusNotFound)
		}
		return WebhookResult{}, err
	}

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
