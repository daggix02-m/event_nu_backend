package dto

import "time"

// UpdatePreferencesRequest is the PUT body for /users/me/preferences: a full
// replace of the user's interest categories. Must contain at least one id.
type UpdatePreferencesRequest struct {
	CategoryIDs []string `json:"category_ids"`
}

// PreferencesDTO is the shared response shape for GET/PUT preferences.
// UpdatedAt is null before the user ever saved preferences.
type PreferencesDTO struct {
	CategoryIDs         []string   `json:"category_ids"`
	UpdatedAt           *time.Time `json:"updated_at"`
	OnboardingCompleted bool       `json:"onboarding_completed"`
}
