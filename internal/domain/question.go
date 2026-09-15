package domain

import "time"

// Question is an audience question on an event, with optional organizer answer.
type Question struct {
	ID        string
	EventID   string
	UserID    string
	Body      string
	Pinned    bool
	Answer    *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// QuestionWithVote decorates a Question with aggregated vote data.
type QuestionWithVote struct {
	Question
	Votes     int
	VotedByMe bool
}
