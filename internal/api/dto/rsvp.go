package dto

import "time"

type RsvpRequest struct {
	PublicRsvp *bool `json:"public_rsvp"` // defaults to true
}

// RsvpState is the response of RSVP toggles and the per-event state endpoint.
type RsvpState struct {
	Going   bool      `json:"going"`
	Status  string    `json:"status,omitempty"`
	Created time.Time `json:"created_at,omitempty"`
}

// RsvpDTO is an item of GET /me/rsvps.
type RsvpDTO struct {
	EventID    string        `json:"event_id"`
	Status     string        `json:"status"`
	PublicRSVP bool          `json:"public_rsvp"`
	CreatedAt  time.Time     `json:"created_at"`
	Event      *EventSummary `json:"event"` // nil when the event is no longer publicly visible
}
