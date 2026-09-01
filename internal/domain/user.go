package domain

import "time"

type UserRole string

const (
	RoleUser  UserRole = "user"
	RoleAdmin UserRole = "admin"
)

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