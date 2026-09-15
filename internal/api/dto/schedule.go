package dto

import "time"

type ScheduleRequest struct {
	Title    string     `json:"title"`
	Speaker  string     `json:"speaker"`
	Stage    string     `json:"stage"`
	StartsAt time.Time  `json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at"`
}

type ScheduleUpdateRequest struct {
	Title    *string    `json:"title"`
	Speaker  *string    `json:"speaker"`
	Stage    *string    `json:"stage"`
	StartsAt *time.Time `json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at"`
}

type ScheduleDTO struct {
	ID        string     `json:"id"`
	EventID   string     `json:"event_id"`
	Title     string     `json:"title"`
	Speaker   *string    `json:"speaker"`
	Stage     *string    `json:"stage"`
	StartsAt  time.Time  `json:"starts_at"`
	EndsAt    *time.Time `json:"ends_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}
