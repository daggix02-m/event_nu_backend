package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	seg "github.com/segmentio/kafka-go"
)

// EnsureTopic creates the relay topic if missing. Composition-root startup
// calls this so the relay never races auto-creation with its first facts (a
// fresh topic's metadata is not ready in the same request Window the first
// write needs — the broker answers Unknown Topic Or Partition). An existing
// topic makes this a no-op.
func EnsureTopic(ctx context.Context, brokers []string, topic string, logger *slog.Logger) error {
	client := &seg.Client{Addr: seg.TCP(brokers...), Timeout: 10 * time.Second}
	res, err := client.CreateTopics(ctx, &seg.CreateTopicsRequest{
		Topics: []seg.TopicConfig{{
			Topic:             topic,
			NumPartitions:     1,
			ReplicationFactor: 1,
		}},
	})
	if err != nil {
		return fmt.Errorf("kafka create topic %s: %w", topic, err)
	}
	if terr, ok := res.Errors[topic]; ok && terr != nil && !errors.Is(terr, seg.TopicAlreadyExists) {
		return fmt.Errorf("kafka create topic %s: %w", topic, terr)
	}
	if logger != nil {
		logger.Info("kafka topic ensured", "topic", topic)
	}
	return nil
}
