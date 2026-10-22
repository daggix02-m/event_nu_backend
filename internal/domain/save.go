package domain

import "time"

// SaveFolder is a user-defined bucket for saved events. Folders are private to
// their owner and enforced by RLS.
type SaveFolder struct {
	ID        string
	UserID    string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Save struct {
	UserID    string
	EventID   string
	FolderID  *string
	CreatedAt time.Time
}

// SaveWithEvent couples a save with the (possibly hidden) event it points at.
// Event fields are COALESCE'd and may be empty when the event is no longer
// publicly visible under RLS.
type SaveWithEvent struct {
	Save
	EventTitle string
	EventStart time.Time
	EventTime  *time.Time
}

type Share struct {
	ID        string
	UserID    string
	EventID   string
	Channel   string
	Ref       string
	CreatedAt time.Time
}

// OrganizerFollow records that a user follows an organizer. The composite
// (user_id, organizer_id) key makes the toggle naturally idempotent.
type OrganizerFollow struct {
	UserID      string
	OrganizerID string
	CreatedAt   time.Time
}
