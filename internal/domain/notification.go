package domain

import "time"

// Notification is an inbox record. Dispatch (push) is handled by the Phase 17
// worker; here notifications are created by hooks and read by the recipient.
// The dispatch_* fields are worker bookkeeping and are zeroed in the inbox
// reads.
type Notification struct {
	ID        string
	UserID    string
	Type      string
	Title     string
	Body      string
	Data      map[string]any
	ReadAt    *time.Time
	CreatedAt time.Time

	DispatchAttempts int
	NextDispatchAt   *time.Time
}

// Reminder is a request to be notified ahead of an event. Records only — the
// worker dispatches due reminders in Phase 17.
type Reminder struct {
	ID        string
	EventID   string
	UserID    string
	RemindAt  time.Time
	Status    string
	CreatedAt time.Time
}

// ReminderWithEvent couples a reminder with the event it targets. Event fields
// are empty when the event is no longer publicly visible under RLS.
type ReminderWithEvent struct {
	Reminder
	EventTitle string
	EventTime  *time.Time // nil when the event is hidden
}

// EventFilters narrows a full-text event search. Only the Query is required;
// the other fields are optional and applied when non-nil/non-empty.
type EventFilters struct {
	Query      string
	CategoryID string
	DateFrom   *time.Time
	DateTo     *time.Time
	Latitude   *float64
	Longitude  *float64
	RadiusKM   *float64
}
