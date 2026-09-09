package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/repository"
)

// failingIdemStore returns an error from a configurable set of methods, used to
// exercise the middleware's error branches that the happy-path suite skips.
type failingIdemStore struct {
	createErr   error
	getErr      error
	completeErr error
}

func (f *failingIdemStore) Create(ctx context.Context, userID, key, requestHash, operation string, ttl time.Duration) error {
	return f.createErr
}

func (f *failingIdemStore) Get(ctx context.Context, userID, key string) (*repository.IdempotencyRecord, error) {
	return nil, f.getErr
}

func (f *failingIdemStore) Complete(ctx context.Context, userID, key, status string, code int, body []byte) error {
	return f.completeErr
}

// TestIdempotencyCreateUnexpectedError500 proves a Create error other than
// ErrIdempotencyExists yields a 500.
func TestIdempotencyCreateUnexpectedError500(t *testing.T) {
	store := &failingIdemStore{createErr: errors.New("db down")}
	rec := httptest.NewRecorder()

	Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run when Create fails unexpectedly")
	})).ServeHTTP(rec, idemRequest(http.MethodPost, "/api/v1/events", `{"title":"x"}`, "key-1"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// TestIdempotencyGetError500 proves a Get failure after a Create conflict yields
// a 500 rather than a silent pass-through.
func TestIdempotencyGetError500(t *testing.T) {
	store := &failingIdemStore{
		createErr: repository.ErrIdempotencyExists,
		getErr:    errors.New("db down"),
	}
	rec := httptest.NewRecorder()

	Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run when Get fails")
	})).ServeHTTP(rec, idemRequest(http.MethodPost, "/api/v1/events", `{"title":"x"}`, "key-1"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// TestIdempotencyCompleteErrorKeepsResponse proves a Complete failure after the
// handler already wrote a successful response is swallowed (the response cannot
// be rewritten to a 5xx); the stored body stays intact.
func TestIdempotencyCompleteErrorKeepsResponse(t *testing.T) {
	store := &failingIdemStore{completeErr: errors.New("db down")}
	rec := httptest.NewRecorder()

	handler := Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"order-1"}`))
	}))

	handler.ServeHTTP(rec, idemRequest(http.MethodPost, "/api/v1/events", `{"title":"x"}`, "key-1"))

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected the already-written 201 to survive a Complete failure, got %d", rec.Code)
	}
	if got := rec.Body.String(); got != `{"id":"order-1"}` {
		t.Fatalf("expected the response body intact, got %q", got)
	}
}

// TestIdempotencyMissingUser500 proves a request with an Idempotency-Key but no
// authenticated user is rejected with 500 (the middleware cannot attribute the
// key without a user).
func TestIdempotencyMissingUser500(t *testing.T) {
	store := newFakeIdemStore()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(`{"title":"x"}`))
	req.Header.Set("Idempotency-Key", "key-1") // no UserIDKey in context

	rec := httptest.NewRecorder()
	Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run without an authenticated user")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}
