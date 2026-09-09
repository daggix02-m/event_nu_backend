package dto

import "time"

type CreateCommentRequest struct {
	Body string `json:"body"`
}

type UpdateCommentRequest struct {
	Body string `json:"body"`
}

type CommentDTO struct {
	ID               string    `json:"id"`
	EventID          string    `json:"event_id"`
	UserID           string    `json:"user_id"`
	Body             string    `json:"body"`
	ModerationStatus string    `json:"moderation_status"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type LikeState struct {
	Liked     bool `json:"liked"`
	LikeCount int  `json:"like_count"`
}
