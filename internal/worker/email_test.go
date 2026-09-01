package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/email"
	"github.com/daggix02-m/event_nu/backend/internal/repository"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func loadEnv(t *testing.T) {
	t.Helper()
	if os.Getenv("DATABASE_URL") != "" {
		return
	}
	dir, _ := os.Getwd()
	for {
		p := filepath.Join(dir, ".env")
		if _, err := os.Stat(p); err == nil {
			_ = godotenv.Load(p)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	loadEnv(t)
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping worker integration tests")
	}
	pool, err := database.NewPool(context.Background(), dsn)
	if err != nil {
		t.Skipf("database unreachable (%v) — skipping worker integration tests", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type fakeBrevo struct {
	mu      sync.Mutex
	calls   int
	statuses []int
	payloads []sendPayload
}

func (f *fakeBrevo) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/smtp/email" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("api-key") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var p sendPayload
		_ = json.Unmarshal(body, &p)

		f.mu.Lock()
		f.calls++
		f.payloads = append(f.payloads, p)
		status := http.StatusCreated
		if len(f.statuses) > 0 {
			status = f.statuses[0]
			f.statuses = f.statuses[1:]
		}
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusCreated {
			fmt.Fprintf(w, `{"messageId":"fake-%d"}`, f.calls)
		} else {
			fmt.Fprintf(w, `{"message":"error"}`)
		}
	}
}

func testConsumer(t *testing.T, pool *pgxpool.Pool, cfg config.Config, sender email.Sender) *EmailConsumer {
	return NewEmailConsumer(testLogger(), cfg, repository.NewOutboxRepository(pool), sender)
}

func TestWorkerSendsAndMarksSent(t *testing.T) {
	pool := newTestPool(t)
	fake := &fakeBrevo{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	cfg := testWorkerConfig()
	client := email.NewBrevoClient("test-key", srv.URL, "Event Nu", "sender@x.com", "reply@x.com")
	consumer := testConsumer(t, pool, cfg, client)

	// Simulate an enqueued email (as the API would).
	outbox := repository.NewOutboxRepository(pool)
	err := outbox.Create(context.Background(), domain.EmailOutbox{
		RecipientEmail: "user@test.example",
		RecipientName:  "user",
		TemplateID:     7,
		Params:         map[string]any{"name": "user"},
		MaxAttempts:    3,
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if err := consumer.processBatch(context.Background()); err != nil {
		t.Fatalf("processBatch: %v", err)
	}

	fake.mu.Lock()
	calls, payloads := fake.calls, fake.payloads
	fake.mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected 1 brevo call, got %d", calls)
	}
	if len(payloads) != 1 || payloads[0].TemplateID != 7 {
		t.Fatalf("unexpected payload: %+v", payloads)
	}
	if payloads[0].Sender.Email != "sender@x.com" || payloads[0].ReplyTo.Email != "reply@x.com" {
		t.Fatalf("expected technical sender + user-facing reply-to, got %+v", payloads[0])
	}

	// Row must be marked sent with the brevo message id.
	var status, msgID string
	err = pool.QueryRow(context.Background(),
		`SELECT status, brevo_message_id FROM email_outbox WHERE recipient_email = 'user@test.example'`).
		Scan(&status, &msgID)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if status != "sent" || msgID == "" {
		t.Fatalf("expected sent + message id, got status=%q msg=%q", status, msgID)
	}

	// email_logs must have a record.
	var logStatus string
	err = pool.QueryRow(context.Background(),
		`SELECT status FROM email_logs WHERE recipient_email = 'user@test.example'`).
		Scan(&logStatus)
	if err != nil {
		t.Fatalf("read email_logs: %v", err)
	}
	if logStatus != "sent" {
		t.Fatalf("expected sent log, got %q", logStatus)
	}
}

func TestWorkerRetriesThenSucceeds(t *testing.T) {
	pool := newTestPool(t)
	fake := &fakeBrevo{statuses: []int{500, 503}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	cfg := testWorkerConfig()
	cfg.EmailRetryBaseDelay = time.Millisecond
	client := email.NewBrevoClient("test-key", srv.URL, "Event Nu", "sender@x.com", "reply@x.com")
	consumer := testConsumer(t, pool, cfg, client)

	outbox := repository.NewOutboxRepository(pool)
	if err := outbox.Create(context.Background(), domain.EmailOutbox{
		RecipientEmail: "retry@test.example",
		TemplateID:     7,
		Params:         map[string]any{},
		MaxAttempts:    5,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Pass 1: 500 → retry scheduled.
	if err := consumer.processBatch(context.Background()); err != nil {
		t.Fatalf("pass1: %v", err)
	}
	// Pass 2: 503 → retry scheduled.
	if err := consumer.processBatch(context.Background()); err != nil {
		t.Fatalf("pass2: %v", err)
	}
	// Pass 3: success.
	if err := consumer.processBatch(context.Background()); err != nil {
		t.Fatalf("pass3: %v", err)
	}

	var status, attempts string
	_ = pool.QueryRow(context.Background(),
		`SELECT status, attempts::text FROM email_outbox WHERE recipient_email = 'retry@test.example'`).
		Scan(&status, &attempts)
	if status != "sent" {
		t.Fatalf("expected final sent, got %q (attempts=%s)", status, attempts)
	}
}

func TestWorkerDeadLettersOnPermanentFailure(t *testing.T) {
	pool := newTestPool(t)
	fake := &fakeBrevo{statuses: []int{400, 400, 400}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	cfg := testWorkerConfig()
	cfg.EmailMaxAttempts = 3
	client := email.NewBrevoClient("test-key", srv.URL, "Event Nu", "sender@x.com", "reply@x.com")
	consumer := testConsumer(t, pool, cfg, client)

	outbox := repository.NewOutboxRepository(pool)
	if err := outbox.Create(context.Background(), domain.EmailOutbox{
		RecipientEmail: "dead@test.example",
		TemplateID:     7,
		Params:         map[string]any{},
		MaxAttempts:    3,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	for i := 0; i < 4; i++ {
		_ = consumer.processBatch(context.Background())
	}

	var status, lastErr string
	err := pool.QueryRow(context.Background(),
		`SELECT status, last_error FROM email_outbox WHERE recipient_email = 'dead@test.example'`).
		Scan(&status, &lastErr)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if status != "failed" {
		t.Fatalf("expected dead-lettered failed, got %q", status)
	}
	if lastErr == "" {
		t.Fatal("expected last_error recorded")
	}
}

type sendPayload struct {
	Sender     struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"sender"`
	ReplyTo struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"replyTo"`
	TemplateID int `json:"templateId"`
}