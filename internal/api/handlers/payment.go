package handlers

import (
	"io"
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/service"
)

// PaymentHandlers expose the provider callback webhook. It runs under the
// trusted 'service' role inside the request transaction; the raw body is kept
// verbatim so the provider signature can be verified over exactly what the
// provider signed.
type PaymentHandlers struct {
	app *api.Application
	pay *service.PaymentService
}

func NewPaymentHandlers(app *api.Application, pay *service.PaymentService) *PaymentHandlers {
	return &PaymentHandlers{app: app, pay: pay}
}

// ChapaWebhook (POST /webhooks/payments/chapa). Chapa headers: "Chapa-Signature"
// and/or "x-chapa-signature" (HMAC-SHA256 of the raw body; either is accepted).
func (h *PaymentHandlers) ChapaWebhook(w http.ResponseWriter, r *http.Request) {
	const maxBody = 16 << 10 // a full webhook payload is a few KB
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", "Could not read webhook payload.")
		return
	}

	signatures := []string{
		r.Header.Get("Chapa-Signature"),
		r.Header.Get("X-Chapa-Signature"),
	}

	if _, err := h.pay.HandleWebhook(r.Context(), body, signatures...); err != nil {
		// Handles shared.AppError (401 bad signature, 400 bad payload, 404
		// unknown order) and falls through to 500 for provider-verify
		// failures, which Chapa keeps retrying until we can reconcile.
		h.app.AppError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
