package dto

import "time"

type ReviewRequest struct {
	Rating int    `json:"rating"` // 1..5
	Body   string `json:"body"`   // optional, max 5000 chars
}

type ReviewDTO struct {
	ID        string    `json:"id"`
	EventID   string    `json:"event_id"`
	UserID    string    `json:"user_id"`
	Rating    int16     `json:"rating"`
	Body      string    `json:"body"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
