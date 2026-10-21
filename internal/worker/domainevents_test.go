package worker

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/config"
	"github.com/daggix02-m/event_nu_backend/internal/domain"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/kafka"
	"github.com/daggix02-m/event_nu_backend/internal/repository"
	seg "github.com/segmentio/kafka-go"
)

// stubOutbox scripts claim results and records marks.
type stubOutbox struct {
	claims    []domain.DomainEventDelivery
	marked    []string
	failClaim bool
}

func (s *stubOutbox) ClaimBatch(ctx context.Context, n int, lease time.Duration) ([]domain.DomainEventDelivery, error) {
	if s.failClaim {
		return nil, context.DeadlineExceeded
	}
	return s.claims, nil
}

func (s *stubOutbox) MarkSent(ctx context.Context, id string) error {
	s.marked = append(s.marked, id)
	return nil
}

// recordingPublisher fans results out; errors are scripted per event id.
type recordingPublisher struct {
	sent    []domain.DomainEvent
	failKey string
}

func (p *recordingPublisher) Publish(_ context.Context, ev domain.DomainEvent) error {
	if ev.Key == p.failKey {
		return context.DeadlineExceeded
	}
	p.sent = append(p.sent, ev)
	return nil
}

// TestRelayPublishesAndMarks pins the happy path: every claimed row is
// published once and marked sent — no loss, no double-marking.
func TestRelayPublishesAndMarks(t *testing.T) {
	store := &stubOutbox{claims: []domain.DomainEventDelivery{
		{ID: "r-1", Event: domain.DomainEvent{Name: "event.published", Key: "e-1"}},
		{ID: "r-2", Event: domain.DomainEvent{Name: "event.published", Key: "e-2"}},
	}}
	pub := &recordingPublisher{}
	relay := &DomainEventRelay{logger: quietLogger(), cfg: config.Config{DomainEventPollInterval: time.Millisecond}, store: store, pub: pub}
	if err := relay.processBatch(t.Context()); err != nil {
		t.Fatalf("processBatch: %v", err)
	}
	if len(pub.sent) != 2 {
		t.Fatalf("published %d events, want 2", len(pub.sent))
	}
	if len(store.marked) != 2 || store.marked[0] != "r-1" || store.marked[1] != "r-2" {
		t.Fatalf("marked: %v", store.marked)
	}
}

// TestRelayKeepsFailedRowsPending pins at-least-once: a publish failure
// leaves the row pending (never marked), and the relay moves on to the
// next row — one bad fact must not block the batch.
func TestRelayKeepsFailedRowsPending(t *testing.T) {
	store := &stubOutbox{claims: []domain.DomainEventDelivery{
		{ID: "r-1", Event: domain.DomainEvent{Name: "x", Key: "poison"}},
		{ID: "r-2", Event: domain.DomainEvent{Name: "x", Key: "e-2"}},
	}}
	pub := &recordingPublisher{failKey: "poison"}
	relay := &DomainEventRelay{logger: quietLogger(), cfg: config.Config{DomainEventPollInterval: time.Millisecond}, store: store, pub: pub}
	if err := relay.processBatch(t.Context()); err != nil {
		t.Fatalf("processBatch must not abort on one failed publish: %v", err)
	}
	marked := map[string]bool{"r-1": false, "r-2": false}
	for _, id := range store.marked {
		marked[id] = true
	}
	if marked["r-1"] || !marked["r-2"] {
		t.Fatalf("poison row must stay pending, healthy row must be marked: %v", store.marked)
	}
}

func quietLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// TestRelayEndToEndAgainstKafka is doubly gated (DATABASE_URL +
// KAFKA_SMOKE_BROKERS): it writes a fact through the real outbox
// repository, relays it with the real Kafka producer, and reads it back —
// the full transactional-outbox pipe in one test.
func TestRelayEndToEndAgainstKafka(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	brokersRaw := os.Getenv("KAFKA_SMOKE_BROKERS")
	if dsn == "" || brokersRaw == "" {
		t.Skip("DATABASE_URL or KAFKA_SMOKE_BROKERS not set — skipping the full-pipe test")
	}
	brokers := strings.Split(brokersRaw, ",")
	topic := "eventnu.relay." + time.Now().Format("150405")

	pool, err := database.NewPoolWithRole(context.Background(), dsn, "service")
	if err != nil {
		t.Skipf("database unreachable (%v) — skipping", err)
	}
	defer pool.Close()
	store := repository.NewDomainEventRepository(pool)
	producer := kafka.NewProducer(kafka.ProducerConfig{
		Brokers: brokers, Topic: topic, DeliveryTimeout: 10 * time.Second,
	}, nil)
	defer producer.Close()

	// EnsureTopic so the relay's first write can't race topic metadata
	// availability (the broker answers Unknown Topic Or Partition before a
	// brand-new topic settles).
	if err := kafka.EnsureTopic(context.Background(), brokers, topic, nil); err != nil {
		t.Fatalf("ensure topic: %v", err)
	}

	// Real stderr logger so a silent publish failure can't masquerade as a
	// consume timeout.
	relayLog := slog.New(slog.NewTextHandler(os.Stderr, nil))
	relay := NewDomainEventRelay(relayLog, config.Config{DomainEventPollInterval: time.Millisecond}, store, producer)

	ev := domain.DomainEvent{
		Name:       "event.published",
		Key:        "relay-e2e-1",
		OccurredAt: time.Now().UTC(),
		Payload:    []byte(`{"event_id":"relay-e2e-1"}`),
	}
	if err := store.Write(context.Background(), ev, ""); err != nil {
		t.Fatalf("write fact: %v", err)
	}
	if err := relay.processBatch(context.Background()); err != nil {
		t.Fatalf("relay batch: %v", err)
	}

	// Delivered rows leave the pending set: no double-ship, no orphans.
	after, err := store.ClaimBatch(context.Background(), 50, 5*time.Second)
	if err != nil {
		t.Fatalf("post-batch claim: %v", err)
	}
	for _, c := range after {
		if c.Event.Key == ev.Key {
			t.Fatal("delivered row was reclaimed (would double-ship)")
		}
	}

	// Read the topic back: the fact made it to the wire.
	r := seg.NewReader(seg.ReaderConfig{
		Brokers: brokers, Topic: topic,
		MinBytes: 1, MaxBytes: 10 << 20, MaxWait: time.Second,
		StartOffset: seg.FirstOffset,
	})
	defer r.Close()
	rctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := r.ReadMessage(rctx); err != nil {
		t.Fatalf("consume: %v", err)
	}
}
