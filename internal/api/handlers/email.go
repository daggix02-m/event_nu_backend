package handlers

import (
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

type EmailHandlers struct {
	app *api.Application
}

func NewEmailHandlers(app *api.Application) *EmailHandlers {
	return &EmailHandlers{app: app}
}

type verifyRequest struct {
	Code string `json:"code"`
}

// VerifyPOST is used by the app: sends the code from the email in the body.
func (h *EmailHandlers) VerifyPOST(w http.ResponseWriter, r *http.Request) {
	var req verifyRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	h.verify(w, r, req.Code)
}

// VerifyGET backs the clickable link in the verification email:
// GET /api/v1/auth/verify?code=XXXXXX
func (h *EmailHandlers) VerifyGET(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	h.verify(w, r, code)
}

func (h *EmailHandlers) verify(w http.ResponseWriter, r *http.Request, code string) {
	if err := h.app.Email.VerifyCode(r.Context(), code); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]bool{"verified": true})
}