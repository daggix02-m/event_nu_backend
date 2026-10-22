package domain

import (
	"context"
	"time"
)

type DomainEvent struct {
	Name string

	Key string

	OccurredAt time.Time

	Payload []byte
}

// EventPublisher accepts domain events for asynchronous delivery. The
// contract is deliberately narrow: fire-and-forget with at-least-once intent.
// The relay worker (not the request path) implements this port — the request
// writes to the outbox, the relay ships.
type EventPublisher interface {
	Publish(ctx context.Context, ev DomainEvent) error
}

// DomainEventDelivery is a claimed outbox row pending publication: the
// envelope plus the row's storage identity.
type DomainEventDelivery struct {
	ID       string
	Event    DomainEvent
	Attempts int
}

// DomainEventWriter records a fact inside the caller's transaction. The
// repository reads the request transaction from the context (the same
// mechanism repositories use for queries), which is what makes the insert
// atomic with the state change — the transactional part of the outbox.
type DomainEventWriter interface {
	Write(ctx context.Context, ev DomainEvent, ownerUserID string) error
}

// DomainEventOutbox is the relay-side seam: claim due rows so concurrent
// workers never double-claim, and mark them delivered.
type DomainEventOutbox interface {
	ClaimBatch(ctx context.Context, batchSize int, lease time.Duration) ([]DomainEventDelivery, error)
	MarkSent(ctx context.Context, id string) error
}
