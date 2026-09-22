package dto

import (
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
)

type ApplyOrganizerRequest struct {
	RequestedName string `json:"requested_name"`
	RequestedSlug string `json:"requested_slug"`
	Bio           string `json:"bio"`
}

type OrganizerApplicationDTO struct {
	ID            string    `json:"id"`
	RequestedName string    `json:"requested_name"`
	RequestedSlug string    `json:"requested_slug"`
	Bio           string    `json:"bio"`
	Status        string    `json:"status"`
	ReviewNotes   string    `json:"review_notes"`
	CreatedAt     time.Time `json:"created_at"`
}

// AdminOrganizerApplicationDTO is the admin queue item: the applicant's
// identity plus review metadata, alongside the applicant-submitted fields.
type AdminOrganizerApplicationDTO struct {
	ID                string     `json:"id"`
	UserID            string     `json:"user_id"`
	RequestedName     string     `json:"requested_name"`
	RequestedSlug     string     `json:"requested_slug"`
	Bio               string     `json:"bio"`
	Status            string     `json:"status"`
	ReviewNotes       string     `json:"review_notes"`
	ApplicantUsername *string    `json:"applicant_username"`
	ApplicantEmail    *string    `json:"applicant_email"`
	ReviewedBy        *string    `json:"reviewed_by"`
	ReviewedAt        *time.Time `json:"reviewed_at"`
	CreatedAt         time.Time  `json:"created_at"`
}

func NewAdminOrganizerApplicationDTO(a *domain.OrganizerApplication) AdminOrganizerApplicationDTO {
	return AdminOrganizerApplicationDTO{
		ID:                a.ID,
		UserID:            a.UserID,
		RequestedName:     a.RequestedName,
		RequestedSlug:     a.RequestedSlug,
		Bio:               a.Bio,
		Status:            a.Status,
		ReviewNotes:       a.ReviewNotes,
		ApplicantUsername: a.ApplicantUsername,
		ApplicantEmail:    a.ApplicantEmail,
		ReviewedBy:        a.ReviewedBy,
		ReviewedAt:        a.ReviewedAt,
		CreatedAt:         a.CreatedAt,
	}
}

type ReviewApplicationRequest struct {
	Notes string `json:"notes"`
}

type CreateVenueRequest struct {
	Name        string  `json:"name"`
	Address     string  `json:"address"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	PlaceID     string  `json:"place_id"`
	City        string  `json:"city"`
	CountryCode string  `json:"country_code"`
}

type VenueDTO struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Address     string  `json:"address"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	City        string  `json:"city"`
	CountryCode string  `json:"country_code"`
	Status      string  `json:"status"`
}

type CategoryDTO struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// OrganizerDTO is the public organizer profile. follower_count and
// followed_by_me are populated from organizer_follows on read.
type OrganizerDTO struct {
	ID            string    `json:"id"`
	Slug          string    `json:"slug"`
	Name          string    `json:"name"`
	Bio           string    `json:"bio"`
	Status        string    `json:"status"`
	FollowerCount int       `json:"follower_count"`
	FollowedByMe  bool      `json:"followed_by_me"`
	CreatedAt     time.Time `json:"created_at"`
}

// FollowState is the response of follow/unfollow toggles.
type FollowState struct {
	FollowerCount int  `json:"follower_count"`
	FollowedByMe  bool `json:"followed_by_me"`
}
