package dto

import "time"

type CreateQuestionRequest struct {
	Body string `json:"body"`
}

type QuestionDTO struct {
	ID        string    `json:"id"`
	EventID   string    `json:"event_id"`
	UserID    string    `json:"user_id"`
	Body      string    `json:"body"`
	Pinned    bool      `json:"pinned"`
	Answer    *string   `json:"answer,omitempty"`
	Votes     int       `json:"votes"`
	VotedByMe bool      `json:"voted_by_me"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type PinRequest struct {
	Pinned *bool `json:"pinned"`
}

type AnswerRequest struct {
	Answer string `json:"answer"`
}
