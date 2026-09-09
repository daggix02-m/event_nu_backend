package dto

import "time"

type CreateSaveFolderRequest struct {
	Name string `json:"name"`
}

type UpdateSaveFolderRequest struct {
	Name string `json:"name"`
}

type SaveFolderDTO struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type SaveEventRequest struct {
	FolderID *string `json:"folder_id"`
}

// SaveState is the response of save/unsave toggles.
type SaveState struct {
	Saved    bool    `json:"saved"`
	FolderID *string `json:"folder_id"`
}

type SaveDTO struct {
	EventID   string        `json:"event_id"`
	FolderID  *string       `json:"folder_id"`
	CreatedAt time.Time     `json:"created_at"`
	Event     *EventSummary `json:"event"` // nil when the saved event is no longer publicly visible
}

// EventSummary is the lightweight representation of a saved event.
type EventSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	StartsAt  time.Time `json:"starts_at"`
	Status    string    `json:"status"`
	IsVisible bool      `json:"is_visible"`
}

type ShareEventRequest struct {
	Channel string `json:"channel"` // e.g. "whatsapp", "copy-link", "invite" — the surface/channel the share was initiated from
	Ref     string `json:"ref"`     // optional route/surface identifier, e.g. "home-feed"
}
