package domain

import "context"

// UserStore is the persisted-identity port AuthService depends on.
type UserStore interface {
	Create(ctx context.Context, email, passwordHash, username string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetByID(ctx context.Context, id string) (*User, error)
	UpdateProfile(ctx context.Context, id string, username, bio, photoURL *string) (*User, error)
}

// SessionStore is the session/refresh-token port AuthService depends on.
type SessionStore interface {
	Create(ctx context.Context, s *Session) error
	ConsumeForRotation(ctx context.Context, refreshHash string) (*Session, error)
	Revoke(ctx context.Context, refreshHash string) error
}
