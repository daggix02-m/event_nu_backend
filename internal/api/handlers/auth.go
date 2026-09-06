package handlers

import (
	"net/http"
	"strings"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
	"github.com/daggix02-m/event_nu/backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/service"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

type AuthHandlers struct {
	app  *api.Application
	auth *service.AuthService
}

func NewAuthHandlers(app *api.Application, auth *service.AuthService) *AuthHandlers {
	return &AuthHandlers{app: app, auth: auth}
}

func (h *AuthHandlers) Register(w http.ResponseWriter, r *http.Request) {
	var req dto.RegisterRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	userAgent, ip := clientMeta(r)
	session, err := h.auth.Register(r.Context(), req.Email, req.Password, req.Username, userAgent, ip)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	h.writeAuthResponse(w, r, session)

	// Fire-and-forget transactional emails (welcome + verification) via the
	// outbox — these never block the response and the worker retries failures.
	if err := h.app.Email.SendWelcome(r.Context(), session.User); err != nil {
		h.app.Logger.Warn("welcome enqueue failed", "user_id", session.User.ID, "error", err.Error())
	}
	if _, err := h.app.Email.SendVerification(r.Context(), session.User); err != nil {
		h.app.Logger.Warn("verification enqueue failed", "user_id", session.User.ID, "error", err.Error())
	}
}

func (h *AuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	var req dto.LoginRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	userAgent, ip := clientMeta(r)
	session, err := h.auth.Login(r.Context(), req.Email, req.Password, userAgent, ip)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	h.writeAuthResponse(w, r, session)
}

func (h *AuthHandlers) Refresh(w http.ResponseWriter, r *http.Request) {
	var req dto.RefreshRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	userAgent, ip := clientMeta(r)
	session, err := h.auth.Refresh(r.Context(), req.RefreshToken, userAgent, ip)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	h.writeAuthResponse(w, r, session)
}

func (h *AuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	var req dto.LogoutRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	if err := h.auth.Logout(r.Context(), req.RefreshToken); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (h *AuthHandlers) Me(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	if userID == "" {
		h.app.ClientError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}

	user, err := h.auth.GetUser(r.Context(), userID)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, toUserDTO(user))
}

func (h *AuthHandlers) writeAuthResponse(w http.ResponseWriter, r *http.Request, session *service.UserSession) {
	resp := dto.AuthResponse{
		User:         toUserDTO(session.User),
		AccessToken:  session.Tokens.AccessToken,
		RefreshToken: session.Tokens.RefreshToken,
		ExpiresIn:    int64(session.Tokens.ExpiresIn.Seconds()),
	}
	shared.WriteJSON(w, http.StatusOK, resp)
}

func toUserDTO(u *domain.User) dto.UserDTO {
	return dto.UserDTO{
		ID:         u.ID,
		Email:      u.Email,
		Username:   u.Username,
		Bio:        u.Bio,
		PhotoURL:   u.PhotoURL,
		Role:       string(u.Role),
		IsVerified: u.IsVerified,
		CreatedAt:  u.CreatedAt,
	}
}

func clientMeta(r *http.Request) (userAgent, ip string) {
	userAgent = r.Header.Get("User-Agent")
	ip = r.RemoteAddr
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		if first := strings.Split(forwarded, ",")[0]; first != "" {
			ip = first
		}
	}
	return userAgent, ip
}
