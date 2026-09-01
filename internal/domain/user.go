package domain

import "time"

type UserRole string

const (
	RoleUser  UserRole = "user"
	RoleAdmin UserRole = "admin"
)

// EmailOutbox is a queued transactional email awaiting worker delivery.
type EmailOutbox struct {
	ID            string
	RecipientEmail string
	RecipientName  string
	TemplateID     int
	Params         map[string]any
	Attempts       int
	MaxAttempts    int
	NextRetryAt    time.Time
	IdempotencyKey string
}

type User struct {
	ID           string
	Email        string
	PasswordHash string
	Username     string
	Bio          string
	PhotoURL     string
	Role         UserRole
	IsVerified   bool
	Status       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time
}

type Session struct {
	ID         string
	UserID     string
	RefreshHash string
	UserAgent  string
	IPHash     string
	DeviceName string
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}