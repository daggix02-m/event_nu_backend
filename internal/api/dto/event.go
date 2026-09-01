package dto

import "time"

type CreateEventRequest struct {
	VenueID      *string `json:"venue_id"`
	CategoryID   *string `json:"category_id"`
	Title        string  `json:"title"`
	Description  string  `json:"description"`
	StartsAt     string  `json:"starts_at"` // RFC3339
	EndsAt       *string `json:"ends_at"`   // RFC3339, optional
	PriceIsFree  bool    `json:"price_is_free"`
	PriceDisplay string  `json:"price_display"`
	ActionType   string  `json:"action_type"`
	ActionTarget string  `json:"action_target"`
	MaxAttendees *int    `json:"max_attendees"`
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
	CreatedAt        time.Time  `json:"created_at"`
}