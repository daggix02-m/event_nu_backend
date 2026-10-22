package domain

import (
	"context"
	"time"
)

// Driven ports for transactional email (hexagonal slice 2). The EmailService
// core enqueues outbox records and redeems verification codes through these
// interfaces; package repository implements them. The consuming side of the
// outbox keeps its port local to the worker package — the worker is an
// application-level process, and the domain only needs the enqueue contract.

// EmailOutboxStore is the enqueue side of the transactional outbox.
type EmailOutboxStore interface {
	Create(ctx context.Context, m EmailOutbox) error
}

type EmailCodeStore interface {
	Create(ctx context.Context, userID, purpose, codeHash string, expiresAt time.Time, maxAttempts int) error
	Consume(ctx context.Context, codeHash, purpose string) (string, error)
}

type UserVerificationStore interface {
	UpdateVerified(ctx context.Context, userID string) error
}
