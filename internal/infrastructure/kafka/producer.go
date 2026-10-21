package kafka

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/domain"
	seg "github.com/segmentio/kafka-go"
)

// Producer is the Kafka implementation of domain.EventPublisher: one logical
// broker cluster + topic, keyed writes so facts about the same aggregate
// land on the same partition and keep their order.
type Producer struct {
	writer  *seg.Writer
	timeout time.Duration
	logger  *slog.Logger
}

// ProducerConfig pins the adapter's knobs at the composition root.
type ProducerConfig struct {
	// Brokers is the seed list; the client discovers the rest of the cluster.
	Brokers []string
	// Topic receives every event this producer emits (adapter-level topic;
	// per-event routing would want more writers, not a second contract).
	Topic string
	// DeliveryTimeout bounds each WriteMessages call. Short-ish, because the
	// publisher fires from a request path that must not pile up workers.
	DeliveryTimeout time.Duration
}

func NewProducer(cfg ProducerConfig, logger *slog.Logger) *Producer {
	timeout := cfg.DeliveryTimeout
	if timeout <= 0 {
		// Sensible default: long enough to ride a broker hiccup, short
		// enough not to pile up fire-and-forget goroutines.
		timeout = 10 * time.Second
	}
	return &Producer{
		writer: &seg.Writer{
			Addr:                   seg.TCP(cfg.Brokers...),
			Topic:                  cfg.Topic,
			Balancer:               &seg.Hash{},
			RequiredAcks:           seg.RequireAll,
			AllowAutoTopicCreation: true,
			// Bounded retries so a broker hiccup doesn't block the
			// fire-and-forget request goroutine for minutes.
			MaxAttempts: 3,
		},
		timeout: timeout,
		logger:  logger,
	}
}

// Publish writes one event, blocking until the broker acks (or the context
// expires). Callers wanting async semantics wrap this in a goroutine; the
// adapter stays synchronous so error surfaces precisely here.
func (p *Producer) Publish(ctx context.Context, ev domain.DomainEvent) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	err := p.writer.WriteMessages(ctx, seg.Message{
		Key:   []byte(ev.Key),
		Value: ev.Payload,
		Time:  ev.OccurredAt,
	})
	if err != nil {
		return fmt.Errorf("kafka publish %s: %w", ev.Name, err)
	}
	return nil
}

func (p *Producer) Close() error {
	if p.logger != nil {
		p.logger.Info("kafka producer closing", "topic", p.writer.Topic)
	}
	return p.writer.Close()
}

// Compile-time proof the adapter satisfies the core port.
var _ domain.EventPublisher = (*Producer)(nil)
