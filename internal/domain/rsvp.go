package domain

import "time"

type Rsvp struct {
	ID         string
	EventID    string
	UserID     string
	Status     string
	Quantity   int
	PublicRsvp bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// RsvpWithEvent couples an RSVP with the (possibly hidden) event it targets.
// Event fields are empty when the event is no longer publicly visible under RLS.
type RsvpWithEvent struct {
	Rsvp
	EventTitle string
	EventTime  *time.Time
}

type Review struct {
	ID        string
	EventID   string
	UserID    string
	Rating    int16
	Body      string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Report struct {
	ID             string
	ReporterUserID string
	EntityType     string
	EntityID       string
	ReasonCode     string
	Description    string
	Status         string
	Resolution     *string
	ResolvedAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time

	ReporterUsername *string
	ReporterEmail    *string
	ReporterPhotoURL *string
}
