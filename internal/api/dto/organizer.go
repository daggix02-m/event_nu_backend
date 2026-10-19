package dto

import (
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/domain"
)

type ApplyOrganizerRequest struct {
	RequestedName string `json:"requested_name"`
	RequestedSlug string `json:"requested_slug"`
	Bio           string `json:"bio"`
}

// CreateAdminOrganizerRequest is the console's direct organizer provisioning
// payload. Status is optional (defaults to active in the service); slug, if
// given, is normalized through slugify like the apply flow.
type CreateAdminOrganizerRequest struct {
	Name        string  `json:"name"`
	Slug        string  `json:"slug"`
	Bio         string  `json:"bio"`
	OwnerUserID *string `json:"owner_user_id"`
	LogoMediaID *string `json:"logo_media_id"`
	Status      string  `json:"status"`
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
	ApplicantUsername *string    `json:"applicant_username,omitempty"`
	ApplicantEmail    *string    `json:"applicant_email,omitempty"`
	ApplicantPhotoURL *string    `json:"applicant_photo_url,omitempty"`
	ReviewedBy        *string    `json:"reviewed_by,omitempty"`
	ReviewedAt        *time.Time `json:"reviewed_at,omitempty"`
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
		ApplicantPhotoURL: a.ApplicantPhotoURL,
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

// CreateAdminVenueRequest is the console's venue provisioning payload, bound
// to an existing organizer (the event wizard sends these same fields with the
// event's resolved/created organizer).
type CreateAdminVenueRequest struct {
	OrganizerID string  `json:"organizer_id"`
	Name        string  `json:"name"`
	Address     string  `json:"address"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	PlaceID     string  `json:"place_id"`
	City        string  `json:"city"`
	CountryCode string  `json:"country_code"`
}

// AdminVenueDTO is the admin-facing venue row (no active-only restriction —
// the console sees suspended venues too).
type AdminVenueDTO struct {
	ID          string    `json:"id"`
	OrganizerID string    `json:"organizer_id"`
	Name        string    `json:"name"`
	Address     string    `json:"address"`
	Latitude    float64   `json:"latitude"`
	Longitude   float64   `json:"longitude"`
	PlaceID     string    `json:"place_id,omitempty"`
	City        string    `json:"city,omitempty"`
	CountryCode string    `json:"country_code,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

func NewAdminVenueDTO(v *domain.Venue) AdminVenueDTO {
	out := AdminVenueDTO{
		ID:          v.ID,
		Name:        v.Name,
		Address:     v.Address,
		Latitude:    v.Latitude,
		Longitude:   v.Longitude,
		PlaceID:     v.PlaceID,
		City:        v.City,
		CountryCode: v.CountryCode,
		Status:      v.Status,
		CreatedAt:   v.CreatedAt,
	}
	if v.OrganizerID != nil {
		out.OrganizerID = *v.OrganizerID
	}
	return out
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

// AdminOrganizerDTO is the admin-facing organizer row: full fields plus the
// resolved logo URL and the number of events. owner_user_id is null for
// auto-provisioned standalone profiles. LogoURL and EventCount are populated
// by the handler (LogoURL resolves the organizer_logo asset; EventCount counts
// events across all lifecycle statuses).
type AdminOrganizerDTO struct {
	ID          string    `json:"id"`
	OwnerUserID *string   `json:"owner_user_id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Bio         string    `json:"bio"`
	Status      string    `json:"status"`
	LogoURL     string    `json:"logo_url,omitempty"`
	EventCount  int       `json:"event_count"`
	CreatedAt   time.Time `json:"created_at"`
}

func NewAdminOrganizerDTO(o *domain.Organizer) AdminOrganizerDTO {
	return AdminOrganizerDTO{
		ID:          o.ID,
		OwnerUserID: o.OwnerUserID,
		Slug:        o.Slug,
		Name:        o.Name,
		Bio:         o.Bio,
		Status:      o.Status,
		CreatedAt:   o.CreatedAt,
	}
}

// AdminOrganizerDetailDTO is the console's organizer detail view. AdminOrganizerDTO
// embeds and flattens the profile fields; EventCount is set by the handler and
// RecentEvents holds the organizer's newest events across all statuses.
type AdminOrganizerDetailDTO struct {
	AdminOrganizerDTO
	RecentEvents []AdminEventDTO `json:"recent_events"`
}

// FollowState is the response of follow/unfollow toggles.
type FollowState struct {
	FollowerCount int  `json:"follower_count"`
	FollowedByMe  bool `json:"followed_by_me"`
}

// MyOrganizerDTO is the authenticated caller's own organizer profile.
// Unlike AdminOrganizerDTO, it does not echo owner_user_id (which is redundant
// on a caller-scoped endpoint). Unlike the public OrganizerDTO, it does not
// carry social fields (follower_count, followed_by_me), but carries logo_url
// and event_count so the organizer dashboard can render its logo and stats.
type MyOrganizerDTO struct {
	ID         string    `json:"id"`
	Slug       string    `json:"slug"`
	Name       string    `json:"name"`
	Bio        string    `json:"bio"`
	Status     string    `json:"status"`
	LogoURL    string    `json:"logo_url,omitempty"`
	EventCount int       `json:"event_count"`
	CreatedAt  time.Time `json:"created_at"`
}
