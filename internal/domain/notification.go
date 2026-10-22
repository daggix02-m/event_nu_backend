package domain

import "time"

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
	EventTime  *time.Time
}

type EventFilters struct {
	Query      string
	CategoryID string
	DateFrom   *time.Time
	DateTo     *time.Time
	Latitude   *float64
	Longitude  *float64
	RadiusKM   *float64
}
