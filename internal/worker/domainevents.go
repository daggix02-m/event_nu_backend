package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/config"
	"github.com/daggix02-m/event_nu_backend/internal/domain"
)

// relayLease bounds how long a claimed row stays unshippable by the rest of
// the relay pool. The normal path is far shorter; the lease exists for a
// crashed worker whose claimed rows must become claimable again.
const relayLease = 10 * time.Second

const relayBatchSize = 25

// DomainEventRelay bridges the transactional outbox to Kafka. The request
// path wrote the row atomically with the aggregate change; the relay is the
// only component that touches the broker. Delivery is at-least-once: a
// publish error leaves the row pending, and the expired lease re-queues it.
// Consumers must therefore treat the fact as idempotent-to-apply (the payload
// carries the event id; the wire key keeps ordering per event).
type DomainEventRelay struct {
	logger *slog.Logger
	cfg    config.Config
	store  domain.DomainEventOutbox
	pub    domain.EventPublisher
}

func NewDomainEventRelay(logger *slog.Logger, cfg config.Config, store domain.DomainEventOutbox, pub domain.EventPublisher) *DomainEventRelay {
	return &DomainEventRelay{logger: logger, cfg: cfg, store: store, pub: pub}
}

func (r *DomainEventRelay) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.cfg.DomainEventPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := r.processBatch(ctx); err != nil {
				r.logger.Error("domain event relay batch failed", "error", err.Error())
			}
		}
	}
}

func (r *DomainEventRelay) processBatch(ctx context.Context) error {
	claims, err := r.store.ClaimBatch(ctx, relayBatchSize, relayLease)
	if err != nil {
		return err
	}
	for _, d := range claims {
		pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := r.pub.Publish(pctx, d.Event)
		cancel()
		if err != nil {
			// Leave the row pending: the lease expiry re-queues it. Logged,
			// not fatal — delivery is retried, never silently dropped.
			r.logger.Error("kafka publish failed (will retry)",
				"event_id", d.ID, "name", d.Event.Name, "key", d.Event.Key, "error", err.Error())
			continue
		}
		if err := r.store.MarkSent(ctx, d.ID); err != nil {
			r.logger.Error("mark domain event sent failed (row stays leased until expiry)",
				"event_id", d.ID, "error", err.Error())
		}
	}
	return nil
}
