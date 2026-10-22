package handlers

import (
	"io"
	"net/http"

	"github.com/daggix02-m/event_nu_backend/internal/api"
	"github.com/daggix02-m/event_nu_backend/internal/service"
)

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
	const maxBody = 16 << 10
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

		h.app.AppError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
