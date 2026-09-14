package dto

import "time"

// CreateMomentRequest is the POST body for sharing a moment.
type CreateMomentRequest struct {
	MediaAssetID string `json:"media_asset_id"`
	Caption      string `json:"caption"`
}

// MomentDTO is an item in the moments gallery list.
type MomentDTO struct {
	ID           string    `json:"id"`
	EventID      string    `json:"event_id"`
	UserID       string    `json:"user_id"`
	MediaAssetID string    `json:"media_asset_id"`
	Caption      string    `json:"caption,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// MomentAttendeeDTO is an item in the attendee directory.
type MomentAttendeeDTO struct {
	UserID      string    `json:"user_id"`
	DisplayName string    `json:"display_name"`
	AvatarURL   string    `json:"avatar_url,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}
