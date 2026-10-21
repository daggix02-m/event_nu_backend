package domain

import (
	"context"
	"time"
)

// DomainEvent is one business fact that crossed a real lifecycle boundary —
// the moment a consumer becomes interesting beyond its own request. The
// envelope is core-owned (hexagonal): transports serialize the payload, the
// names and ordering key are the contract.
type DomainEvent struct {
	// Name identifies the fact ("event.published"). Namespaced dot-lowercase
	// so cross-language consumers can route on it.
	Name string

	// Key is the ordering/partitioning key — the aggregate ID, so every fact
	// about one event lands on the same Kafka partition in order.
	Key string

	// OccurredAt is when the fact became true, not when it shipped.
	OccurredAt time.Time

	// Payload is a self-contained JSON body; consumers get the ID plus enough
	// context to decide without a join when possible.
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
