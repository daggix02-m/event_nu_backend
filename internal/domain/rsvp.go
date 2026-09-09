package domain

import "time"

// Rsvp records a user's intent to attend an event. Capacity is reserved
// atomically by attempt_event_rsvp() at the SQL layer, so this Go type is
// write-thin (toggles call the SQL helper; reads scan rows).
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
	EventTime  *time.Time // nil when the event is hidden
}

// Review is a rating + optional body left on an event by an attendee. At most
// one review per (user, event) — enforced by a UNIQUE constraint.
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

// Report is a user moderation report against a polymorphic target
// (event|user|venue|comment in Phase 12c). It flips the target's
// moderation_status to 'under_review' and never auto-hides content.
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
}
