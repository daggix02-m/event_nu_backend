package domain

import "time"

// EventSession is a schedule slot within an event (e.g. a talk, workshop).
type EventSession struct {
	ID        string
	EventID   string
	Title     string
	Speaker   *string
	Stage     *string
	StartsAt  time.Time
	EndsAt    *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}
