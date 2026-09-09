package dto

import "time"

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
