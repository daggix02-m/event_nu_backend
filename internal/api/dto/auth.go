package dto

import (
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
)

type RegisterRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Username string `json:"username"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type LogoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type UserDTO struct {
	ID         string    `json:"id"`
	Email      string    `json:"email"`
	Username   string    `json:"username"`
	Bio        string    `json:"bio"`
	PhotoURL   string    `json:"photo_url"`
	Role       string    `json:"role"`
	IsVerified bool      `json:"is_verified"`
	CreatedAt  time.Time `json:"created_at"`
	// OnboardingCompleted is true once the user finished (or explicitly
	// skipped) the interactive onboarding flow.
	OnboardingCompleted bool `json:"onboarding_completed"`
}

// AdminUserDTO is the admin directory row: the self-view profile plus status
// and no onboarding flag (irrelevant to an admin). Username/bio/photo_url are
// editable by admins; email is read-only (email changes run the public
// verification flow).
type AdminUserDTO struct {
	ID         string    `json:"id"`
	Email      string    `json:"email"`
	Username   string    `json:"username"`
	Bio        string    `json:"bio"`
	PhotoURL   string    `json:"photo_url"`
	Role       string    `json:"role"`
	IsVerified bool      `json:"is_verified"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

func NewAdminUserDTO(u *domain.User) AdminUserDTO {
	return AdminUserDTO{
		ID:         u.ID,
		Email:      u.Email,
		Username:   u.Username,
		Bio:        u.Bio,
		PhotoURL:   u.PhotoURL,
		Role:       string(u.Role),
		IsVerified: u.IsVerified,
		Status:     u.Status,
		CreatedAt:  u.CreatedAt,
	}
}

type AuthResponse struct {
	User         UserDTO `json:"user"`
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresIn    int64   `json:"expires_in_seconds"`
}

// UpdateProfileRequest is a PATCH body for /users/me: every field is optional,
// nil means "leave unchanged". An empty body (all nil) is rejected as a
// validation error so the request is never a silent no-op.
type UpdateProfileRequest struct {
	Username *string `json:"username"`
	Bio      *string `json:"bio"`
	PhotoURL *string `json:"photo_url"`
}

// UpdateAdminUserRequest is the console's user-directory edit. Nil fields are
// left unchanged; role/status changes are subject to the self-modification
// guard; email changes stay out of scope.
type UpdateAdminUserRequest struct {
	Username *string `json:"username"`
	Bio      *string `json:"bio"`
	PhotoURL *string `json:"photo_url"`
	Role     *string `json:"role"`
	Status   *string `json:"status"`
}
