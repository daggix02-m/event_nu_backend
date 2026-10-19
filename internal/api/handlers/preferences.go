package handlers

import (
	"net/http"

	"github.com/daggix02-m/event_nu_backend/internal/api"
	"github.com/daggix02-m/event_nu_backend/internal/api/dto"
	"github.com/daggix02-m/event_nu_backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu_backend/internal/domain"
	"github.com/daggix02-m/event_nu_backend/internal/service"
	"github.com/daggix02-m/event_nu_backend/internal/shared"
)

type PreferencesHandlers struct {
	app   *api.Application
	prefs *service.PreferencesService
}

func NewPreferencesHandlers(app *api.Application, prefs *service.PreferencesService) *PreferencesHandlers {
	return &PreferencesHandlers{app: app, prefs: prefs}
}

// Get returns the authenticated user's category preferences.
func (h *PreferencesHandlers) Get(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	if userID == "" {
		h.app.ClientError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}

	prefs, user, err := h.prefs.Get(r.Context(), userID)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, toPreferencesDTO(prefs, user))
}

// Replace overwrites the user's category preferences (full replace). Picking
// interests completes onboarding.
func (h *PreferencesHandlers) Replace(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	if userID == "" {
		h.app.ClientError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}

	var req dto.UpdatePreferencesRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	prefs, user, err := h.prefs.Replace(r.Context(), userID, req.CategoryIDs)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, toPreferencesDTO(prefs, user))
}

// SkipOnboarding stamps onboarding as complete without storing interests.
func (h *PreferencesHandlers) SkipOnboarding(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	if userID == "" {
		h.app.ClientError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}

	user, err := h.prefs.SkipOnboarding(r.Context(), userID)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"status":               "skipped",
		"onboarding_completed": user.OnboardingCompletedAt != nil,
	})
}

func toPreferencesDTO(prefs service.Preferences, user *domain.User) dto.PreferencesDTO {
	return dto.PreferencesDTO{
		CategoryIDs:         prefs.CategoryIDs,
		UpdatedAt:           prefs.UpdatedAt,
		OnboardingCompleted: user.OnboardingCompletedAt != nil,
	}
}
