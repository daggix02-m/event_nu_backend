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
// per-user like/save state (meaningless for an admin list) and without the
// media URL joins (fetched on demand when a poster is needed).
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
		CreatedAt:        e.CreatedAt,
	}
}
