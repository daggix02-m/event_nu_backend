package domain

import "context"

// This file holds the driven ports (hexagonal architecture): interfaces
// defined by the core and implemented by persistence adapters in package
// repository. The dependency direction is one-way — the core never imports
// the adapters — and the guard test internal/arch_test.go enforces it.
//
// Slices: auth is slice 1; later slices migrate the remaining services the
// same way (see the exception ledger in internal/arch_test.go). Narrower
// store views some services define locally (e.g. the admin users directory)
// stay in the service package intentionally — consumers keep declaring the
// smallest interface they need; these ports are the full write contracts.

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
