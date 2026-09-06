package worker

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/email"
	"github.com/daggix02-m/event_nu/backend/internal/repository"
	"github.com/jackc/pgx/v5/pgxpool"
)

// countingSender records every provider call per recipient, with optional
// per-send failure and an optional delay that honours ctx cancellation. It can
// also notify a test the moment a recipient's send starts (shutdown test).
type countingSender struct {
	mu     sync.Mutex
	sent   map[string]int
	err    error
	delay  time.Duration
	onSend func(recipient string)
}

func (s *countingSender) Send(ctx context.Context, m email.Message) (string, error) {
	if s.onSend != nil {
		s.onSend(m.RecipientEmail)
	}
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	s.mu.Lock()
	if s.sent == nil {
		s.sent = make(map[string]int)
	}
	s.sent[m.RecipientEmail]++
	s.mu.Unlock()
	if s.err != nil {
		return "", s.err
	}
	return "mid-" + m.RecipientEmail, nil
}

func (s *countingSender) calls(recipient string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sent[recipient]
}

func (s *countingSender) totalCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, v := range s.sent {
		n += v
	}
	return n
}

// faultyMarkSent is a store whose commit step fails even though the provider
// already accepted the message — simulating "crashed/errored after the send
// call but before the sent row committed".
type faultyMarkSent struct {
	*repository.OutboxRepository
}

func (f *faultyMarkSent) MarkSent(ctx context.Context, id, messageID string) error {
	return fmt.Errorf("injected MarkSent failure")
}

func enqueueOutbox(t *testing.T, repo *repository.OutboxRepository, recipient string, maxAttempts int) {
	t.Helper()
	if err := repo.Create(context.Background(), domain.EmailOutbox{
		RecipientEmail: recipient,
		TemplateID:     7,
		Params:         map[string]any{},
		MaxAttempts:    maxAttempts,
	}); err != nil {
		t.Fatalf("enqueue %s: %v", recipient, err)
	}
}

func outboxState(t *testing.T, pool *pgxpool.Pool, recipient string) (status string, attempts, sentLogs int) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT status, attempts FROM email_outbox WHERE recipient_email = $1`, recipient).
		Scan(&status, &attempts)
	if err != nil {
		t.Fatalf("read outbox for %s: %v", recipient, err)
	}
	err = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM email_logs WHERE recipient_email = $1 AND status = 'sent'`, recipient).
		Scan(&sentLogs)
	if err != nil {
		t.Fatalf("read logs for %s: %v", recipient, err)
	}
	return status, attempts, sentLogs
}

// forceLeaseExpiry pushes next_retry_at into the past so a previously claimed
// row becomes claimable again, deterministically simulating lease expiry
// without sleeping on a real clock.
func forceLeaseExpiry(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE email_outbox SET next_retry_at = now() - interval '1 second' WHERE id = $1`, id); err != nil {
		t.Fatalf("bump next_retry_at: %v", err)
	}
}

// TestWorkerFailureAfterProviderKeepsRowRetryable: the provider accepts the
// message but the commit step fails. The invariant is at-least-once with no
// half-written state — the row must remain 'pending' (retryable), the attempt
// counter untouched, and no 'sent' record committed.
func TestWorkerFailureAfterProviderKeepsRowRetryable(t *testing.T) {
	pool := newTestPool(t)
	purgeEmailTables(t, pool)

	addr := "fail-inject@test.example"
	real := repository.NewOutboxRepository(pool)
	enqueueOutbox(t, real, addr, 3)

	sender := &countingSender{}
	store := &faultyMarkSent{OutboxRepository: real}
	consumer := &EmailConsumer{logger: testLogger(), cfg: testWorkerConfig(), outbox: store, sender: sender}

	if err := consumer.processBatch(context.Background()); err != nil {
		t.Fatalf("processBatch: %v", err)
	}

	if sender.calls(addr) != 1 {
		t.Fatalf("expected the provider to be called once, got %d", sender.calls(addr))
	}
	status, attempts, sentLogs := outboxState(t, pool, addr)
	if status != "pending" {
		t.Fatalf("row must remain retryable 'pending', got %q", status)
	}
	if attempts != 0 {
		t.Fatalf("attempts must be untouched by a failed commit, got %d", attempts)
	}
	if sentLogs != 0 {
		t.Fatalf("no 'sent' log may be committed: got %d", sentLogs)
	}
}

// TestWorkerConcurrentClaimSingleSend: N workers claiming the same pending
// pool must each claim a row exactly once (SKIP LOCKED lease) — every row is
// sent exactly once, no double-send, none skipped.
func TestWorkerConcurrentClaimSingleSend(t *testing.T) {
	pool := newTestPool(t)
	purgeEmailTables(t, pool)

	const rows = 40
	real := repository.NewOutboxRepository(pool)
	for i := 0; i < rows; i++ {
		enqueueOutbox(t, real, fmt.Sprintf("conc-%02d@test.example", i), 3)
	}

	cfg := testWorkerConfig()
	cfg.EmailBatchSize = 20
	sender := &countingSender{}
	consumer := NewEmailConsumer(testLogger(), cfg, real, sender)

	const workers = 6
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 10; round++ {
				if err := consumer.processBatch(context.Background()); err != nil {
					t.Errorf("processBatch: %v", err)
				}
				if sender.totalCalls() >= rows {
					return
				}
			}
		}()
	}
	wg.Wait()

	if sender.totalCalls() != rows {
		t.Fatalf("expected exactly %d provider calls, got %d", rows, sender.totalCalls())
	}
	for i := 0; i < rows; i++ {
		addr := fmt.Sprintf("conc-%02d@test.example", i)
		if n := sender.calls(addr); n != 1 {
			t.Fatalf("recipient %s sent %d times — double-send", addr, n)
		}
	}

	var pending, sent, failed int
	err := pool.QueryRow(context.Background(),
		`SELECT
			count(*) FILTER (WHERE status = 'pending'),
			count(*) FILTER (WHERE status = 'sent'),
			count(*) FILTER (WHERE status = 'failed')
		FROM email_outbox WHERE recipient_email LIKE 'conc-%@test.example'`).
		Scan(&pending, &sent, &failed)
	if err != nil {
		t.Fatalf("aggregate outbox: %v", err)
	}
	if pending != 0 || failed != 0 || sent != rows {
		t.Fatalf("expected %d sent / 0 pending / 0 failed, got %d / %d / %d", rows, sent, pending, failed)
	}

	var sentLogs int
	err = pool.QueryRow(context.Background(),
		`SELECT count(DISTINCT outbox_id) FROM email_logs WHERE recipient_email LIKE 'conc-%@test.example' AND status = 'sent'`).
		Scan(&sentLogs)
	if err != nil {
		t.Fatalf("aggregate logs: %v", err)
	}
	if sentLogs != rows {
		t.Fatalf("expected %d distinct sent log rows, got %d", rows, sentLogs)
	}
}

// TestWorkerLeaseExpiryRecoversClaim: a worker that claimed a row and then
// died mid-send must not lose the email. After the lease expires the row is
// claimable again and the message is eventually sent.
func TestWorkerLeaseExpiryRecoversClaim(t *testing.T) {
	pool := newTestPool(t)
	purgeEmailTables(t, pool)

	addr := "lease@test.example"
	real := repository.NewOutboxRepository(pool)
	enqueueOutbox(t, real, addr, 3)

	// Worker A claims the row, then dies before sending (lease not released).
	claimed, err := real.ClaimBatch(context.Background(), 5, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("expected 1 claimed row, got %d", len(claimed))
	}
	status, attempts, _ := outboxState(t, pool, addr)
	if status != "pending" || attempts != 0 {
		t.Fatalf("dead claimer must leave a fresh pending row, got status=%q attempts=%d", status, attempts)
	}

	// Lease expires.
	forceLeaseExpiry(t, pool, claimed[0].ID)

	// Worker B restarts and re-claims the same row.
	sender := &countingSender{}
	consumer := NewEmailConsumer(testLogger(), testWorkerConfig(), real, sender)
	if err := consumer.processBatch(context.Background()); err != nil {
		t.Fatalf("recovery processBatch: %v", err)
	}

	if sender.calls(addr) != 1 {
		t.Fatalf("recovered delivery must call the provider once, got %d", sender.calls(addr))
	}
	status, _, sentLogs := outboxState(t, pool, addr)
	if status != "sent" {
		t.Fatalf("expected recovered row to be sent, got %q", status)
	}
	if sentLogs != 1 {
		t.Fatalf("expected exactly 1 sent log row, got %d", sentLogs)
	}
}

// TestWorkerGracefulShutdownInterruptedSendStaysRetryable: cancelling the
// worker context mid-send must stop the loop cleanly and leave the in-flight
// claim in a retryable state — never half-written. Recovery then delivers it.
func TestWorkerGracefulShutdownInterruptedSendStaysRetryable(t *testing.T) {
	pool := newTestPool(t)
	purgeEmailTables(t, pool)

	addr := "shutdown@test.example"
	real := repository.NewOutboxRepository(pool)
	enqueueOutbox(t, real, addr, 3)

	sendStarted := make(chan string, 1)
	sender := &countingSender{delay: 300 * time.Millisecond, onSend: func(recipient string) {
		sendStarted <- recipient
	}}
	cfg := testWorkerConfig()
	cfg.EmailPollInterval = 20 * time.Millisecond
	consumer := NewEmailConsumer(testLogger(), cfg, real, sender)

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- consumer.Run(ctx) }()

	// Wait until the send is genuinely in flight, then shut down mid-send.
	select {
	case got := <-sendStarted:
		if got != addr {
			t.Fatalf("unexpected send: %s", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("send never started")
	}
	cancel()

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("consumer.Run did not return after cancel")
	}
	if sender.calls(addr) != 0 {
		t.Fatalf("interrupted send must not reach the provider, got %d calls", sender.calls(addr))
	}

	// The interrupted claim rolled back: still pending+retryable, no sent log.
	var status string
	err := pool.QueryRow(context.Background(),
		`SELECT status FROM email_outbox WHERE recipient_email = $1`, addr).Scan(&status)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if status != "pending" {
		t.Fatalf("interrupted send must stay retryable 'pending', got %q", status)
	}
	_, attempts, sentLogs := outboxState(t, pool, addr)
	if attempts != 0 {
		t.Fatalf("attempts must be untouched, got %d", attempts)
	}
	if sentLogs != 0 {
		t.Fatalf("no sent log may be committed, got %d", sentLogs)
	}

	// No loss: once the lease expires, a fresh worker delivers the email.
	var id string
	err = pool.QueryRow(context.Background(),
		`SELECT id FROM email_outbox WHERE recipient_email = $1`, addr).Scan(&id)
	if err != nil {
		t.Fatalf("read id: %v", err)
	}
	forceLeaseExpiry(t, pool, id)

	rec := NewEmailConsumer(testLogger(), cfg, real, sender)
	if err := rec.processBatch(context.Background()); err != nil {
		t.Fatalf("recovery processBatch: %v", err)
	}
	if sender.calls(addr) != 1 {
		t.Fatalf("expected exactly 1 provider call after recovery, got %d", sender.calls(addr))
	}
	status, _, sentLogs = outboxState(t, pool, addr)
	if status != "sent" || sentLogs != 1 {
		t.Fatalf("expected sent + 1 log after recovery, got status=%q logs=%d", status, sentLogs)
	}
}
