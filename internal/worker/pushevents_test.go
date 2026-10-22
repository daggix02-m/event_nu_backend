package worker

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/config"
	"github.com/daggix02-m/event_nu_backend/internal/domain"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/kafka"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/push"
	seg "github.com/segmentio/kafka-go"
)

// stubFollowers scripts follower lists per organizer.
type stubFollowers struct {
	byOrganizer map[string][]string
}

func (s *stubFollowers) FollowerIDs(_ context.Context, organizerID string) ([]string, error) {
	return s.byOrganizer[organizerID], nil
}

// stubDevices scripts devices per user and records prunes.
type stubDevices struct {
	byUser map[string][]*domain.Device

	mu     sync.Mutex
	pruned []string
}

func (s *stubDevices) ListByUser(_ context.Context, userID string) ([]*domain.Device, error) {
	return s.byUser[userID], nil
}

func (s *stubDevices) DeleteByToken(_ context.Context, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruned = append(s.pruned, token)
	return nil
}

// stubEvents resolves titles by id.
type stubEvents struct{ titles map[string]string }

func (s *stubEvents) GetByID(_ context.Context, id string) (*domain.Event, error) {
	return &domain.Event{ID: id, Title: s.titles[id]}, nil
}

// captureSender records full push messages so the test can inspect titles
// and deep-link data, not just count sends.
type captureSender struct {
	mu   sync.Mutex
	all  []push.Message
	errs map[string]error
}

func (s *captureSender) Name() string { return "capture" }

func (s *captureSender) Send(_ context.Context, m push.Message) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.errs[m.Token]; err != nil {
		return "", err
	}
	s.all = append(s.all, m)
	return "mid-" + m.Token, nil
}

func (s *captureSender) messages() []push.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]push.Message(nil), s.all...)
}

func prunedTokens(s *stubDevices) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.pruned...)
}

// fanoutForTest wires the fanout with scripted in-memory seams.
func fanoutForTest(t *testing.T, followers map[string][]string, devices map[string][]*domain.Device, titles map[string]string, sender *captureSender) (*EventPublishedFanout, *stubDevices) {
	t.Helper()
	sink := &stubDevices{byUser: devices}
	f := NewEventPublishedFanout(quietLogger(), config.Config{KafkaPushGroup: "push-fanout", KafkaTopicEvents: "eventnu.test"},
		&stubFollowers{byOrganizer: followers}, sink, sink, &stubEvents{titles: titles}, sender)
	return f, sink
}

// eventFetcherFunc adapts a function to the fanout's eventFetcher seam.
type eventFetcherFunc func(ctx context.Context, id string) (*domain.Event, error)

func (f eventFetcherFunc) GetByID(ctx context.Context, id string) (*domain.Event, error) {
	return f(ctx, id)
}

// TestEventPublishedFanoutGroup exercises the full consumer-group loop
// against a real broker (gated on KAFKA_SMOKE_BROKERS): Run joins a unique
// group on a unique topic, facts are produced until one lands (LastOffset
// skips anything produced before the group settled — join races are expected,
// not bugs), the matching push must arrive with title + deep-link data, and
// context cancellation must stop the loop cleanly.
func TestEventPublishedFanoutGroup(t *testing.T) {
	brokersRaw := os.Getenv("KAFKA_SMOKE_BROKERS")
	if brokersRaw == "" {
		t.Skip("KAFKA_SMOKE_BROKERS not set — skipping the group smoke test")
	}
	brokers := strings.Split(brokersRaw, ",")
	stamp := time.Now().Format("150405")
	topic := "eventnu.fanout." + stamp
	group := "push-fanout-test-" + stamp

	if err := kafka.EnsureTopic(context.Background(), brokers, topic, nil); err != nil {
		t.Fatalf("ensure topic: %v", err)
	}

	sender := &captureSender{}
	sink := &stubDevices{byUser: map[string][]*domain.Device{"u-1": {{Token: "tok-live"}}}}
	events := eventFetcherFunc(func(_ context.Context, id string) (*domain.Event, error) {
		return &domain.Event{ID: id, Title: "Live Pub: " + id}, nil
	})
	f := NewEventPublishedFanout(quietLogger(),
		config.Config{KafkaBrokers: brokers, KafkaTopicEvents: topic, KafkaPushGroup: group},
		&stubFollowers{byOrganizer: map[string][]string{"org-live": {"u-1"}}},
		sink, sink, events, sender)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- f.Run(ctx) }()

	producer := kafka.NewProducer(kafka.ProducerConfig{
		Brokers: brokers, Topic: topic, DeliveryTimeout: 10 * time.Second,
	}, nil)
	defer producer.Close()

	delivered := ""
	for i := 0; i < 10 && delivered == ""; i++ {
		id := fmt.Sprintf("e-live-%d", i)
		fact := domain.DomainEvent{
			Name:       "event.published",
			Key:        id,
			OccurredAt: time.Now().UTC(),
			Payload:    []byte(fmt.Sprintf(`{"event_id":%q,"organizer_id":"org-live"}`, id)),
		}
		if err := producer.Publish(context.Background(), fact); err != nil {
			t.Fatalf("publish fact: %v", err)
		}
		// Give this fact a window to arrive before producing the next.
		until := time.Now().Add(2 * time.Second)
		for time.Now().Before(until) && delivered == "" {
			for _, m := range sender.messages() {
				if m.Data["event_id"] == id {
					delivered = id
					if m.Title != "Live Pub: "+id {
						t.Fatalf("push title: %q", m.Title)
					}
					if m.Data["organizer_id"] != "org-live" {
						t.Fatalf("push deep-link data: %v", m.Data)
					}
				}
			}
			if delivered == "" {
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
	if delivered == "" {
		t.Fatal("fanout never delivered a produced fact within the budget")
	}

	// Graceful stop: cancel returns nil from Run promptly.
	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("fanout run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("fanout run did not stop after cancel")
	}
}

// TestFanoutDeliversToFollowerDevices pins the notification contract: every
// follower device gets one push carrying the event title and deep-link ids.
func TestFanoutDeliversToFollowerDevices(t *testing.T) {
	sender := &captureSender{}
	devices := map[string][]*domain.Device{
		"u-1": {
			{Token: "tok-1", Platform: "ios"},
			{Token: "tok-2", Platform: "android"},
		},
		"u-2": {{Token: "tok-3", Platform: "web"}},
	}
	f, _ := fanoutForTest(t, map[string][]string{"org-1": {"u-1", "u-2"}}, devices, map[string]string{"e-1": "Grand Opening"}, sender)

	f.handlePublish(t.Context(), seg.Message{Value: []byte(`{"event_id":"e-1","organizer_id":"org-1"}`)})
	msgs := sender.messages()
	if len(msgs) != 3 {
		t.Fatalf("pushed %d, want 3 (one per device)", len(msgs))
	}
	for _, m := range msgs {
		if m.Title != "Grand Opening" {
			t.Fatalf("push title: %q", m.Title)
		}
		if m.Data["event_id"] != "e-1" || m.Data["organizer_id"] != "org-1" {
			t.Fatalf("push deep-link data: %v", m.Data)
		}
	}
}

// TestFanoutDeliversToUnknownEventToo: a fact arriving without a resolvable
// event title still fans out with the placeholder — the notification is more
// valuable than the title (soft-fail).
func TestFanoutDeliversWithMissingEvent(t *testing.T) {
	sender := &captureSender{}
	f, _ := fanoutForTest(t, map[string][]string{"org-1": {"u-1"}},
		map[string][]*domain.Device{"u-1": {{Token: "tok-1"}}}, map[string]string{}, sender)

	f.handlePublish(t.Context(), seg.Message{Value: []byte(`{"event_id":"e-gone","organizer_id":"org-1"}`)})
	msgs := sender.messages()
	if len(msgs) != 1 {
		t.Fatalf("pushed %d, want 1", len(msgs))
	}
	if msgs[0].Title != "An event was published" {
		t.Fatalf("placeholder title: %q", msgs[0].Title)
	}
}

// TestFanoutPrunesInvalidTokens matches the reminder queue hygiene: the
// provider classifies the token as dead, the fanout prunes it and moves on.
func TestFanoutPrunesInvalidTokens(t *testing.T) {
	sender := &captureSender{errs: map[string]error{"tok-dead": &push.Error{Code: push.CodeInvalidToken}}}
	f, sink := fanoutForTest(t,
		map[string][]string{"org-1": {"u-1"}},
		map[string][]*domain.Device{"u-1": {{Token: "tok-dead"}}},
		map[string]string{"e-1": "x"}, sender)

	f.handlePublish(t.Context(), seg.Message{Value: []byte(`{"event_id":"e-1","organizer_id":"org-1"}`)})
	tokens := prunedTokens(sink)
	if len(tokens) != 1 || tokens[0] != "tok-dead" {
		t.Fatalf("pruned tokens: %v", tokens)
	}
}

// TestFanoutIgnoresUnparsableFacts: a malformed fact must not wedge the
// group NOR push to nobody — logged then skipped.
func TestFanoutIgnoresUnparsableFacts(t *testing.T) {
	sender := &captureSender{}
	f, sink := fanoutForTest(t, map[string][]string{"org-1": {"u-1"}},
		map[string][]*domain.Device{"u-1": {{Token: "tok-1"}}}, map[string]string{"e-1": "x"}, sender)

	f.handlePublish(t.Context(), seg.Message{Value: []byte(`{not-json`)})
	if msgs := sender.messages(); len(msgs) != 0 {
		t.Fatalf("malformed fact pushed %d messages", len(msgs))
	}
	if p := prunedTokens(sink); len(p) != 0 {
		t.Fatalf("malformed fact pruned: %v", p)
	}
}
