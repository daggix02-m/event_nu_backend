package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/repository"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// fakeIdemStore is an in-memory stand-in for the idempotency repository,
// recording the writes the middleware issues so tests can assert them.
type fakeIdemStore struct {
	mu        sync.Mutex
	records   map[string]*repository.IdempotencyRecord
	created   []string
	completed []completeCall
}

type completeCall struct {
	key, status string
	code        int
	body        []byte
}

func newFakeIdemStore() *fakeIdemStore {
	return &fakeIdemStore{records: map[string]*repository.IdempotencyRecord{}}
}

func (f *fakeIdemStore) Create(_ context.Context, userID, key, requestHash, operation string, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.records[key]; ok {
		return fmt.Errorf("%w: user_id,key", repository.ErrIdempotencyExists)
	}
	f.records[key] = &repository.IdempotencyRecord{
		UserID: userID, Key: key, RequestHash: requestHash, Operation: operation,
		Status: repository.IdempotencyInProgress, ExpiresAt: time.Now().Add(ttl),
	}
	f.created = append(f.created, key)
	return nil
}

func (f *fakeIdemStore) Get(_ context.Context, _, key string) (*repository.IdempotencyRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.records[key]
	if !ok {
		return nil, shared.ErrNotFound
	}
	cp := *rec
	return &cp, nil
}

func (f *fakeIdemStore) Complete(_ context.Context, _, key, status string, code int, body []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if rec, ok := f.records[key]; ok {
		rec.Status = status
		rec.ResponseCode = code
		rec.ResponseBody = append([]byte{}, body...)
	}
	f.completed = append(f.completed, completeCall{key: key, status: status, code: code, body: append([]byte{}, body...)})
	return nil
}

func (f *fakeIdemStore) completionCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.completed)
}

// idemRequest builds a request with the user authenticated and an optional
// Idempotency-Key header, mirroring the middleware chain's context contract.
func idemRequest(method, path, body, key string) *http.Request {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	ctx := context.WithValue(req.Context(), UserIDKey, "user-1")
	return req.WithContext(ctx)
}

// TestIdempotencyNoKeyPassThrough proves the middleware is opt-in: without an
// Idempotency-Key the request reaches the handler untouched and nothing is
// recorded.
func TestIdempotencyNoKeyPassThrough(t *testing.T) {
	store := newFakeIdemStore()
	called := false
	handler := Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		shared.WriteJSON(w, http.StatusCreated, map[string]any{"id": "x"})
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, idemRequest(http.MethodPost, "/api/v1/events", `{"title":"x"}`, ""))

	if !called {
		t.Fatal("handler must run when no Idempotency-Key is present")
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
	if store.completionCount() != 0 || len(store.created) != 0 {
		t.Fatalf("no-key request must not touch the store: created=%v completed=%d", store.created, store.completionCount())
	}
}

// TestIdempotencyFirstRunStoresResponse proves a new key runs the handler with
// the original body restored and stores the resulting status/body as completed.
func TestIdempotencyFirstRunStoresResponse(t *testing.T) {
	store := newFakeIdemStore()
	var gotBody string
	handler := Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		shared.WriteJSON(w, http.StatusCreated, map[string]any{"id": "abc"})
	}))

	rec := httptest.NewRecorder()
	body := `{"title":"rave","starts_at":"2026-09-08T10:00:00Z"}`
	handler.ServeHTTP(rec, idemRequest(http.MethodPost, "/api/v1/events", body, "key-1"))

	if gotBody != body {
		t.Fatalf("handler must receive the original body: got %q, want %q", gotBody, body)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
	if store.completionCount() != 1 {
		t.Fatalf("expected 1 Complete call, got %d", store.completionCount())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	rec2 := store.records["key-1"]
	if rec2.Status != repository.IdempotencyCompleted || rec2.ResponseCode != 201 {
		t.Fatalf("record not completed: status=%s code=%d", rec2.Status, rec2.ResponseCode)
	}
	if !strings.Contains(string(rec2.ResponseBody), `"id":"abc"`) {
		t.Fatalf("stored body mismatch: %s", rec2.ResponseBody)
	}
}

// TestIdempotencyReplaySameRequest proves same key + same request is replayed
// from storage: the handler is not re-run and the stored response is returned.
func TestIdempotencyReplaySameRequest(t *testing.T) {
	store := newFakeIdemStore()
	path := "/api/v1/events"
	body := `{"title":"rave"}`
	store.records["key-1"] = &repository.IdempotencyRecord{
		UserID:       "user-1",
		Key:          "key-1",
		RequestHash:  idempotencyHash(http.MethodPost+" "+path, []byte(body)),
		Operation:    http.MethodPost + " " + path,
		Status:       repository.IdempotencyCompleted,
		ResponseCode: http.StatusCreated,
		ResponseBody: json.RawMessage(`{"data":{"id":"abc"}}`),
	}

	called := false
	handler := Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, idemRequest(http.MethodPost, path, body, "key-1"))

	if called {
		t.Fatal("replay must not re-run the handler")
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected replayed 201, got %d", rec.Code)
	}
	if got := rec.Body.String(); got != `{"data":{"id":"abc"}}` {
		t.Fatalf("expected stored body replayed, got %s", got)
	}
	if store.completionCount() != 0 {
		t.Fatalf("replay must not call Complete, got %d", store.completionCount())
	}
}

// TestIdempotencyKeyReused defines the different-request conflict: same key,
// differing body -> 409 idempotency_key_reused, handler not run.
func TestIdempotencyKeyReused(t *testing.T) {
	store := newFakeIdemStore()
	path := "/api/v1/events"
	store.records["key-1"] = &repository.IdempotencyRecord{
		UserID:       "user-1",
		Key:          "key-1",
		RequestHash:  idempotencyHash(http.MethodPost+" "+path, []byte(`{"title":"original"}`)),
		Status:       repository.IdempotencyCompleted,
		ResponseCode: http.StatusCreated,
		ResponseBody: json.RawMessage(`{"data":{"id":"abc"}}`),
	}

	called := false
	handler := Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rec := httptest.NewRecorder()
	// Different body than the one that completed the key.
	handler.ServeHTTP(rec, idemRequest(http.MethodPost, path, `{"title":"changed"}`, "key-1"))

	if called {
		t.Fatal("reused key must not re-run the handler")
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "idempotency_key_reused") {
		t.Fatalf("expected idempotency_key_reused body, got %s", rec.Body.String())
	}
}

// TestIdempotencyInProgress defines the racing concurrent-request conflict: an
// existing in_progress row yields 409 idempotency_in_progress (client retries
// once the winner settles).
func TestIdempotencyInProgress(t *testing.T) {
	store := newFakeIdemStore()
	store.records["key-1"] = &repository.IdempotencyRecord{
		UserID:      "user-1",
		Key:         "key-1",
		RequestHash: idempotencyHash(http.MethodPost+" /api/v1/events", []byte(`{"title":"x"}`)),
		Status:      repository.IdempotencyInProgress,
	}

	called := false
	handler := Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, idemRequest(http.MethodPost, "/api/v1/events", `{"title":"x"}`, "key-1"))

	if called {
		t.Fatal("in-progress conflict must not re-run the handler")
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "idempotency_in_progress") {
		t.Fatalf("expected idempotency_in_progress body, got %s", rec.Body.String())
	}
}

// TestIdempotencyFailedWriteNotRecorded proves a handler that returns a 4xx is
// not stored as completed: the key remains free for a corrected retry (the
// request tx would roll the in_progress row back entirely).
func TestIdempotencyFailedWriteNotRecorded(t *testing.T) {
	store := newFakeIdemStore()
	handler := Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		shared.WriteErrorJSON(w, http.StatusBadRequest, "bad_request", "nope")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, idemRequest(http.MethodPost, "/api/v1/events", `{"bad":"json"}`, "key-1"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if store.completionCount() != 0 {
		t.Fatalf("failed writes must not be recorded as completed, got %d Complete calls", store.completionCount())
	}
}

// TestIdempotencyHashDeterministic proves the request fingerprint is stable and
// depends on all three inputs (method, path, body).
func TestIdempotencyHashDeterministic(t *testing.T) {
	body := []byte(`{"title":"rave"}`)
	op := http.MethodPost + " /api/v1/events"
	a := idempotencyHash(op, body)
	b := idempotencyHash(op, body)
	if a != b {
		t.Fatalf("hash must be deterministic: %q != %q", a, b)
	}
	if a == "" || len(a) != 64 {
		t.Fatalf("expected a 64-char sha256 hex, got %q", a)
	}
	if a == idempotencyHash(http.MethodPost+" /api/v1/venues", body) {
		t.Fatal("path must affect the hash")
	}
	if a == idempotencyHash(op, []byte(`{"title":"different"}`)) {
		t.Fatal("body must affect the hash")
	}
}
