package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/push"
)

// fakePushSender records every delivery and can fail per token. It satisfies
// push.Provider.
type fakePushSender struct {
	mu   sync.Mutex
	sent []string
	errs map[string]error
}

func (s *fakePushSender) Name() string { return "fake" }

func (s *fakePushSender) Send(_ context.Context, m push.Message) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.errs[m.Token]; err != nil {
		return "", err
	}
	s.sent = append(s.sent, m.Token)
	return "mid-" + m.Token, nil
}

func (s *fakePushSender) delivered(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.sent {
		if t == token {
			return true
		}
	}
	return false
}

// fakeQueue is an in-memory notificationQueueStore.
type fakeQueue struct {
	mu         sync.Mutex
	claimed    []*domain.Notification
	dispatched []string
	failed     map[string]int
	notify     []string // notification types created (via Notify)
}

func (q *fakeQueue) ClaimForDispatch(_ context.Context, _ int, _ time.Duration) ([]*domain.Notification, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	items := q.claimed
	q.claimed = nil
	return items, nil
}

func (q *fakeQueue) MarkDispatched(_ context.Context, id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.dispatched = append(q.dispatched, id)
	return nil
}

func (q *fakeQueue) MarkDispatchFailed(_ context.Context, id string, attempts int, _ time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.failed == nil {
		q.failed = map[string]int{}
	}
	q.failed[id] = attempts
	return nil
}

func (q *fakeQueue) Notify(_ context.Context, _ string, ntype, _, _ string, _ map[string]any) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.notify = append(q.notify, ntype)
	return "notif-" + ntype, nil
}

func (q *fakeQueue) wasDispatched(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, d := range q.dispatched {
		if d == id {
			return true
		}
	}
	return false
}

func (q *fakeQueue) failedAttempts(id string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.failed[id]
}

// fakeReminderQueue is an in-memory reminderQueueStore.
type fakeReminderQueue struct {
	mu   sync.Mutex
	due  []*domain.Reminder
	sent []string
	fail []string
}

func (q *fakeReminderQueue) ClaimDue(_ context.Context, _ int, _ time.Duration) ([]*domain.Reminder, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	items := q.due
	q.due = nil
	return items, nil
}

func (q *fakeReminderQueue) MarkReminderSent(_ context.Context, id, _ string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sent = append(q.sent, id)
	return nil
}

func (q *fakeReminderQueue) MarkReminderFailed(_ context.Context, id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.fail = append(q.fail, id)
	return nil
}

func (q *fakeReminderQueue) wasSent(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, s := range q.sent {
		if s == id {
			return true
		}
	}
	return false
}

func (q *fakeReminderQueue) wasFailed(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, f := range q.fail {
		if f == id {
			return true
		}
	}
	return false
}

// fakeDevices is an in-memory pushDeviceStore.
type fakeDevices struct {
	users  map[string][]*domain.Device
	pruned []string
}

func (d *fakeDevices) ListByUser(_ context.Context, userID string) ([]*domain.Device, error) {
	return d.users[userID], nil
}

func (d *fakeDevices) DeleteByToken(_ context.Context, token string) error {
	d.pruned = append(d.pruned, token)
	return nil
}

func (d *fakeDevices) wasPruned(token string) bool {
	for _, t := range d.pruned {
		if t == token {
			return true
		}
	}
	return false
}

// fakeReminderEvents is an in-memory reminderEventStore.
type fakeReminderEvents struct {
	events map[string]*domain.Event
	err    error
}

func (e *fakeReminderEvents) GetByID(_ context.Context, id string) (*domain.Event, error) {
	if e.err != nil {
		return nil, e.err
	}
	return e.events[id], nil
}

func pushTestConfig() config.Config {
	return config.Config{
		PushPollInterval:   time.Second,
		PushBatchSize:      50,
		PushMaxAttempts:    5,
		PushRetryBaseDelay: 30 * time.Second,
	}
}

func testNotification(id, userID string, attempts int) *domain.Notification {
	return &domain.Notification{
		ID:               id,
		UserID:           userID,
		Type:             "ticket",
		Title:            "Payment confirmed",
		Body:             "Your ticket is ready.",
		Data:             map[string]any{"order_id": "123"},
		DispatchAttempts: attempts,
	}
}

// TestPushConsumerDispatchSuccess: a notification with one device is sent and
// marked dispatched.
func TestPushConsumerDispatchSuccess(t *testing.T) {
	queue := &fakeQueue{claimed: []*domain.Notification{testNotification("n1", "u1", 0)}}
	devices := &fakeDevices{users: map[string][]*domain.Device{
		"u1": {{ID: "d1", Token: "tok-1", Platform: "android"}},
	}}
	sender := &fakePushSender{}

	c := NewPushConsumer(testLogger(), pushTestConfig(), queue, &fakeReminderQueue{}, devices, &fakeReminderEvents{}, sender)
	if err := c.processNotifications(context.Background()); err != nil {
		t.Fatalf("processNotifications: %v", err)
	}

	if !sender.delivered("tok-1") {
		t.Fatal("expected device tok-1 to receive the push")
	}
	if !queue.wasDispatched("n1") {
		t.Fatal("expected notification n1 marked dispatched")
	}
}

// TestPushConsumerNoDevicesMarkedDispatched: a notification for a user without
// devices must be marked dispatched (nothing to deliver), never re-scanned
// forever.
func TestPushConsumerNoDevicesMarkedDispatched(t *testing.T) {
	queue := &fakeQueue{claimed: []*domain.Notification{testNotification("n1", "u-empty", 0)}}
	devices := &fakeDevices{users: map[string][]*domain.Device{}}
	sender := &fakePushSender{}

	c := NewPushConsumer(testLogger(), pushTestConfig(), queue, &fakeReminderQueue{}, devices, &fakeReminderEvents{}, sender)
	if err := c.processNotifications(context.Background()); err != nil {
		t.Fatalf("processNotifications: %v", err)
	}

	if len(sender.sent) != 0 {
		t.Fatalf("no devices must mean no sends, got %d", len(sender.sent))
	}
	if !queue.wasDispatched("n1") {
		t.Fatal("expected notification marked dispatched")
	}
}

// TestPushConsumerInvalidTokenPrunesDevice: an UNREGISTERED token must prune
// the device and still count as delivered (no retry).
func TestPushConsumerInvalidTokenPrunesDevice(t *testing.T) {
	queue := &fakeQueue{claimed: []*domain.Notification{testNotification("n1", "u1", 0)}}
	devices := &fakeDevices{users: map[string][]*domain.Device{
		"u1": {{ID: "d1", Token: "stale-tok", Platform: "ios"}},
	}}
	sender := &fakePushSender{errs: map[string]error{
		"stale-tok": &push.Error{Code: push.CodeInvalidToken, Msg: "UNREGISTERED"},
	}}

	c := NewPushConsumer(testLogger(), pushTestConfig(), queue, &fakeReminderQueue{}, devices, &fakeReminderEvents{}, sender)
	if err := c.processNotifications(context.Background()); err != nil {
		t.Fatalf("processNotifications: %v", err)
	}

	if !devices.wasPruned("stale-tok") {
		t.Fatal("expected stale token to be pruned")
	}
	if !queue.wasDispatched("n1") {
		t.Fatal("invalid token must not block dispatch")
	}
}

// TestPushConsumerTransientErrorBacksOff: an unavailable provider schedules a
// retry with an incremented attempt instead of dropping the row.
func TestPushConsumerTransientErrorBacksOff(t *testing.T) {
	queue := &fakeQueue{claimed: []*domain.Notification{testNotification("n1", "u1", 0)}}
	devices := &fakeDevices{users: map[string][]*domain.Device{
		"u1": {{ID: "d1", Token: "tok-1", Platform: "android"}},
	}}
	sender := &fakePushSender{errs: map[string]error{
		"tok-1": &push.Error{Code: push.CodeUnavailable, Msg: "UNAVAILABLE"},
	}}

	c := NewPushConsumer(testLogger(), pushTestConfig(), queue, &fakeReminderQueue{}, devices, &fakeReminderEvents{}, sender)
	if err := c.processNotifications(context.Background()); err != nil {
		t.Fatalf("processNotifications: %v", err)
	}

	if queue.wasDispatched("n1") {
		t.Fatal("transient failure must not mark dispatched")
	}
	if got := queue.failedAttempts("n1"); got != 1 {
		t.Fatalf("expected 1 recorded attempt, got %d", got)
	}
}

// TestPushConsumerExhaustedAttemptsGivesUp: once attempts are exhausted the
// row is marked dispatched (no infinite re-scan; inbox copy remains).
func TestPushConsumerExhaustedAttemptsGivesUp(t *testing.T) {
	queue := &fakeQueue{claimed: []*domain.Notification{testNotification("n1", "u1", 4)}}
	devices := &fakeDevices{users: map[string][]*domain.Device{
		"u1": {{ID: "d1", Token: "tok-1", Platform: "android"}},
	}}
	sender := &fakePushSender{errs: map[string]error{
		"tok-1": &push.Error{Code: push.CodeUnavailable, Msg: "UNAVAILABLE"},
	}}
	cfg := pushTestConfig()
	cfg.PushMaxAttempts = 5

	c := NewPushConsumer(testLogger(), cfg, queue, &fakeReminderQueue{}, devices, &fakeReminderEvents{}, sender)
	if err := c.processNotifications(context.Background()); err != nil {
		t.Fatalf("processNotifications: %v", err)
	}

	if !queue.wasDispatched("n1") {
		t.Fatal("exhausted attempts must give up (mark dispatched), not retry")
	}
}

// TestPushConsumerReminderDispatch: a due reminder becomes an inbox
// notification and the reminder is marked sent.
func TestPushConsumerReminderDispatch(t *testing.T) {
	queue := &fakeQueue{}
	reminders := &fakeReminderQueue{due: []*domain.Reminder{{ID: "r1", UserID: "u1", EventID: "e1"}}}
	events := &fakeReminderEvents{events: map[string]*domain.Event{
		"e1": {ID: "e1", Title: "Summer Party"},
	}}

	c := NewPushConsumer(testLogger(), pushTestConfig(), queue, reminders, &fakeDevices{}, events, &fakePushSender{})
	if err := c.processReminders(context.Background()); err != nil {
		t.Fatalf("processReminders: %v", err)
	}

	if !reminders.wasSent("r1") {
		t.Fatal("expected reminder r1 marked sent")
	}
	if len(queue.notify) != 1 || queue.notify[0] != "event_reminder" {
		t.Fatalf("expected one event_reminder notification, got %v", queue.notify)
	}
}

// TestPushConsumerReminderMissingEventFails: when the event is gone the
// reminder is marked failed, not sent.
func TestPushConsumerReminderMissingEventFails(t *testing.T) {
	reminders := &fakeReminderQueue{due: []*domain.Reminder{{ID: "r1", UserID: "u1", EventID: "gone"}}}
	events := &fakeReminderEvents{err: errors.New("not found")}

	c := NewPushConsumer(testLogger(), pushTestConfig(), &fakeQueue{}, reminders, &fakeDevices{}, events, &fakePushSender{})
	if err := c.processReminders(context.Background()); err != nil {
		t.Fatalf("processReminders: %v", err)
	}

	if !reminders.wasFailed("r1") {
		t.Fatal("expected reminder r1 marked failed")
	}
}
