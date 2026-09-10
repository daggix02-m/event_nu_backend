package domain

import "time"

type OrganizerApplication struct {
	ID             string
	UserID         string
	RequestedName  string
	RequestedSlug  string
	Bio            string
	SupportingData map[string]any
	Status         string
	ReviewedBy     *string
	ReviewedAt     *time.Time
	ReviewNotes    string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Organizer struct {
	ID          string
	OwnerUserID *string
	Slug        string
	Name        string
	Bio         string
	Status      string
	CreatedAt   time.Time
	DeletedAt   *time.Time
}

type Venue struct {
	ID          string
	OrganizerID *string
	Name        string
	Address     string
	Latitude    float64
	Longitude   float64
	PlaceID     string
	City        string
	CountryCode string
	Status      string
	CreatedAt   time.Time
	DeletedAt   *time.Time
}

type Category struct {
	ID        string
	Slug      string
	Name      string
	SortOrder int
	IsActive  bool
}

type Event struct {
	ID               string
	OrganizerID      string
	VenueID          *string
	CategoryID       *string
	Title            string
	Description      string
	StartsAt         time.Time
	EndsAt           *time.Time
	PriceIsFree      bool
	PriceDisplay     string
	ActionType       string
	ActionTarget     string
	Status           string
	ModerationStatus string
	MaxAttendees     *int
	PosterMediaID    *string
	TeaserMediaID    *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DeletedAt        *time.Time
}
