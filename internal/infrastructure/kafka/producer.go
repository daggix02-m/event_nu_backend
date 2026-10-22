package kafka

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/domain"
	seg "github.com/segmentio/kafka-go"
)

type Producer struct {
	writer  *seg.Writer
	timeout time.Duration
	logger  *slog.Logger
}

type ProducerConfig struct {
	Brokers []string

	Topic string

	DeliveryTimeout time.Duration
}

func NewProducer(cfg ProducerConfig, logger *slog.Logger) *Producer {
	timeout := cfg.DeliveryTimeout
	if timeout <= 0 {

		timeout = 10 * time.Second
	}
	return &Producer{
		writer: &seg.Writer{
			Addr:                   seg.TCP(cfg.Brokers...),
			Topic:                  cfg.Topic,
			Balancer:               &seg.Hash{},
			RequiredAcks:           seg.RequireAll,
			AllowAutoTopicCreation: true,

			MaxAttempts: 3,
		},
		timeout: timeout,
		logger:  logger,
	}
}

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
