package domain

import "time"

// Device is a registered client device (an FCM registration token) owned by a
// user. Registration updates the row in place (unique token), so signing in on
// the same device simply reassigns ownership.
type Device struct {
	ID         string
	UserID     string
	Token      string
	Platform   string
	LastSeenAt time.Time
	CreatedAt  time.Time
}
