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
// (Until the transactional-outbox slice lands, the wired producer is
// effectively at-most-once: a broker outage drops the fact, nothing
// replays it. Outbox changes the guarantees, not this interface.)
type EventPublisher interface {
	Publish(ctx context.Context, ev DomainEvent) error
}
