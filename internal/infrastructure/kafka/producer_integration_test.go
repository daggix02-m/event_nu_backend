package kafka

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/domain"
	seg "github.com/segmentio/kafka-go"
)

// TestProducerSmokeAgainstKafka is gated: it runs only when
// KAFKA_SMOKE_BROKERS names a reachable seed broker (e.g. a local throwaway
// container). It proves the adapter path end to end — publish, then read the
// fact back with a plain reader keyed by the aggregate id.
func TestProducerSmokeAgainstKafka(t *testing.T) {
	brokersRaw := os.Getenv("KAFKA_SMOKE_BROKERS")
	if brokersRaw == "" {
		t.Skip("KAFKA_SMOKE_BROKERS not set — skipping the real-broker smoke test")
	}
	brokers := strings.Split(brokersRaw, ",")
	topic := "eventnu.smoke." + time.Now().Format("150405")

	p := NewProducer(ProducerConfig{Brokers: brokers, Topic: topic, DeliveryTimeout: 10 * time.Second}, nil)
	defer p.Close()

	ev := domain.DomainEvent{
		Name:       "event.published",
		Key:        "smoke-event-1",
		OccurredAt: time.Now().UTC(),
		Payload:    []byte(`{"event_id":"smoke-event-1"}`),
	}
	// Auto topic creation lives on the writer; give brokers a beat.
	pctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		if err := p.Publish(pctx, ev); err == nil {
			break
		} else if attempt == 2 {
			t.Fatalf("publish: %v", err)
		} else {
			select {
			case <-pctx.Done():
				t.Fatalf("publish retries exhausted: %v", pctx.Err())
			case <-time.After(300 * time.Millisecond):
			}
		}
	}

	r := seg.NewReader(seg.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		// CLI equivalent of `kafka-console-consumer`: group-less read from
		// the (already placed) oldest offset.
		MinBytes:    1,
		MaxBytes:    10 << 20,
		MaxWait:     time.Second,
		StartOffset: seg.FirstOffset,
	})
	defer r.Close()
	rctx, rcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer rcancel()
	m, err := r.ReadMessage(rctx)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if string(m.Key) != "smoke-event-1" || string(m.Value) != `{"event_id":"smoke-event-1"}` {
		t.Fatalf("read back: key=%q value=%q", m.Key, m.Value)
	}
	if m.Topic != topic {
		t.Fatalf("topic: got %q want %q", m.Topic, topic)
	}
}
