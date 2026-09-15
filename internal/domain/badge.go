package domain

import "time"

// Badge types awarded for first-time milestones. A user can earn each at most
// once; award_badge() verifies the milestone against real rows so the strings
// are only presentation labels.
const (
	BadgeFirstRSVP     = "first_rsvp"
	BadgeFirstAttended = "first_attended"
	BadgeFirstTicket   = "first_ticket"
	BadgeFirstMoment   = "first_moment"
)

// Badge is a milestone award earned once by a user.
type Badge struct {
	ID        string
	UserID    string
	BadgeType string
	EarnedAt  time.Time
	Metadata  map[string]any
}
