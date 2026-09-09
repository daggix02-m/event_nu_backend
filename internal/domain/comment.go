package domain

import "time"

// Comment is a user comment on an event. Deleted comments are soft-deleted
// (deleted_at set); blocked comments are hidden from public reads via RLS.
type Comment struct {
	ID               string
	EventID          string
	UserID           string
	Body             string
	ModerationStatus string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DeletedAt        *time.Time
}

// EventLike records that a user liked an event. The composite primary key
// (event_id, user_id) guarantees at most one like per user per event.
type EventLike struct {
	EventID   string
	UserID    string
	CreatedAt time.Time
}
