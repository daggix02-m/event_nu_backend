package dto

import (
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
)

type CreateEventRequest struct {
	VenueID       *string `json:"venue_id"`
	CategoryID    *string `json:"category_id"`
	Title         string  `json:"title"`
	Description   string  `json:"description"`
	StartsAt      string  `json:"starts_at"` // RFC3339
	EndsAt        *string `json:"ends_at"`   // RFC3339, optional
	PriceIsFree   bool    `json:"price_is_free"`
	PriceDisplay  string  `json:"price_display"`
	ActionType    string  `json:"action_type"`
	ActionTarget  string  `json:"action_target"`
	MaxAttendees  *int    `json:"max_attendees"`
	PosterMediaID *string `json:"poster_media_id"`
	TeaserMediaID *string `json:"teaser_media_id"`
}

type EventDTO struct {
	ID               string     `json:"id"`
	OrganizerID      string     `json:"organizer_id"`
	VenueID          *string    `json:"venue_id"`
	CategoryID       *string    `json:"category_id"`
	Title            string     `json:"title"`
	Description      string     `json:"description"`
	StartsAt         time.Time  `json:"starts_at"`
	EndsAt           *time.Time `json:"ends_at"`
	PriceIsFree      bool       `json:"price_is_free"`
	PriceDisplay     string     `json:"price_display"`
	ActionType       string     `json:"action_type"`
	Status           string     `json:"status"`
	ModerationStatus string     `json:"moderation_status"`
	MaxAttendees     *int       `json:"max_attendees"`
	LikeCount        int        `json:"like_count"`
	LikedByMe        bool       `json:"liked_by_me"`
	SavedByMe        bool       `json:"saved_by_me"`
	SavedFolderID    *string    `json:"saved_folder_id"`
	PosterURL        string     `json:"poster_url"`
	TeaserURL        string     `json:"teaser_url"`
	CreatedAt        time.Time  `json:"created_at"`
}

// AdminEventDTO is the moderation queue row: the base event fields without the
// per-user like/save state (meaningless for an admin list). poster_media_id
// and teaser_media_id identify the attached assets; poster_url / teaser_url
// are resolved CDN URLs, populated by the handler (empty when the media is not
// ready or not attached).
type AdminEventDTO struct {
	ID               string     `json:"id"`
	OrganizerID      string     `json:"organizer_id"`
	VenueID          *string    `json:"venue_id"`
	CategoryID       *string    `json:"category_id"`
	Title            string     `json:"title"`
	Description      string     `json:"description"`
	StartsAt         time.Time  `json:"starts_at"`
	EndsAt           *time.Time `json:"ends_at"`
	PriceIsFree      bool       `json:"price_is_free"`
	PriceDisplay     string     `json:"price_display"`
	ActionType       string     `json:"action_type"`
	Status           string     `json:"status"`
	ModerationStatus string     `json:"moderation_status"`
	MaxAttendees     *int       `json:"max_attendees"`
	PosterMediaID    *string    `json:"poster_media_id,omitempty"`
	TeaserMediaID    *string    `json:"teaser_media_id,omitempty"`
	PosterURL        string     `json:"poster_url,omitempty"`
	TeaserURL        string     `json:"teaser_url,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

func NewAdminEventDTO(e *domain.Event) AdminEventDTO {
	return AdminEventDTO{
		ID:               e.ID,
		OrganizerID:      e.OrganizerID,
		VenueID:          e.VenueID,
		CategoryID:       e.CategoryID,
		Title:            e.Title,
		Description:      e.Description,
		StartsAt:         e.StartsAt,
		EndsAt:           e.EndsAt,
		PriceIsFree:      e.PriceIsFree,
		PriceDisplay:     e.PriceDisplay,
		ActionType:       e.ActionType,
		Status:           e.Status,
		ModerationStatus: e.ModerationStatus,
		MaxAttendees:     e.MaxAttendees,
		PosterMediaID:    e.PosterMediaID,
		TeaserMediaID:    e.TeaserMediaID,
		CreatedAt:        e.CreatedAt,
	}
}

// AdminEventOrganizerInput is the inline organizer provisioning payload for the
// event wizard: resolving-or-creating a standalone profile in the same request.
type AdminEventOrganizerInput struct {
	Name        string  `json:"name"`
	Slug        *string `json:"slug"`
	Bio         string  `json:"bio"`
	LogoMediaID *string `json:"logo_media_id"`
}

// AdminEventVenueInput is the inline venue payload, created bound to the event's
// resolved organizer.
type AdminEventVenueInput struct {
	Name        string  `json:"name"`
	Address     string  `json:"address"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	PlaceID     string  `json:"place_id"`
	City        string  `json:"city"`
	CountryCode string  `json:"country_code"`
}

// CreateAdminEventRequest drives the atomic admin event create: organizer
// resolve-or-create, then venue resolve-or-create, then insert, then publish
// (unless publish:false keeps the event a draft).
type CreateAdminEventRequest struct {
	OrganizerID   *string                   `json:"organizer_id"`
	Organizer     *AdminEventOrganizerInput `json:"organizer"`
	VenueID       *string                   `json:"venue_id"`
	Venue         *AdminEventVenueInput     `json:"venue"`
	CategoryID    *string                   `json:"category_id"`
	Title         string                    `json:"title"`
	Description   string                    `json:"description"`
	StartsAt      string                    `json:"starts_at"` // RFC3339
	EndsAt        *string                   `json:"ends_at"`   // RFC3339, optional
	PriceIsFree   bool                      `json:"price_is_free"`
	PriceDisplay  string                    `json:"price_display"`
	ActionType    string                    `json:"action_type"`
	ActionTarget  string                    `json:"action_target"`
	MaxAttendees  *int                      `json:"max_attendees"`
	PosterMediaID *string                   `json:"poster_media_id"`
	TeaserMediaID *string                   `json:"teaser_media_id"`
	Publish       *bool                     `json:"publish"` // default true
}

// ReassignOrganizerRequest moves an event to another organizer, by id or by a
// new inline organizer that is auto-provisioned by the request.
type ReassignOrganizerRequest struct {
	OrganizerID *string                   `json:"organizer_id"`
	Organizer   *AdminEventOrganizerInput `json:"organizer"`
}

// AdminEventOrganizerSummary is the organizer identity shown on an event detail.
type AdminEventOrganizerSummary struct {
	ID      string `json:"id"`
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	LogoURL string `json:"logo_url,omitempty"`
}

// AdminEventDetailDTO joins the admin event row with its resolved organizer
// identity and venue.
type AdminEventDetailDTO struct {
	AdminEventDTO
	Organizer AdminEventOrganizerSummary `json:"organizer"`
	Venue     *AdminVenueDTO             `json:"venue,omitempty"`
}
