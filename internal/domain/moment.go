package domain

import "time"

// Moment is a user-generated media post attached to an event (community gallery).
type Moment struct {
	ID           string
	EventID      string
	UserID       string
	MediaAssetID string
	Caption      string
	CreatedAt    time.Time
}

// MomentAttendee is a public_rsvp attendee returned by the attendee directory.
type MomentAttendee struct {
	UserID      string
	DisplayName string
	AvatarURL   string
	CreatedAt   time.Time
}
