package email

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func message() Message {
	return Message{
		RecipientName:  "Ish",
		RecipientEmail: "ish@x.co",
		TemplateID:     3,
		Params:         map[string]any{"name": "Ish", "code": "123456"},
	}
}

func TestSendSuccessBuildsCorrectRequest(t *testing.T) {
	var got sendPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/smtp/email" {
			t.Errorf("unexpected path %q (trailing slash not trimmed?)", r.URL.Path)
		}
		if r.Header.Get("api-key") != "test-key" {
			t.Errorf("expected api-key header, got %q", r.Header.Get("api-key"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json content-type")
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"messageId":"m-42"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewBrevoClient("test-key", srv.URL+"/", "Event Nu", "from@x.co", "reply@x.co")
	id, err := c.Send(context.Background(), message())
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if id != "m-42" {
		t.Fatalf("expected message id m-42, got %q", id)
	}

	if got.Sender.Name != "Event Nu" || got.Sender.Email != "from@x.co" {
		t.Fatalf("unexpected sender: %+v", got.Sender)
	}
	if got.ReplyTo.Email != "reply@x.co" {
		t.Fatalf("unexpected reply-to: %+v", got.ReplyTo)
	}
	if len(got.To) != 1 || got.To[0].Email != "ish@x.co" || got.To[0].Name != "Ish" {
		t.Fatalf("unexpected recipients: %+v", got.To)
	}
	if got.TemplateID != 3 {
		t.Fatalf("unexpected template id: %+v", got.TemplateID)
	}
	if got.Params == nil {
		t.Fatal("expected params to be forwarded")
	}
}

func TestSendAccepts200And201(t *testing.T) {
	for _, code := range []int{http.StatusOK, http.StatusCreated} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"messageId":"m-ok"}`))
		}))
		c := NewBrevoClient("k", srv.URL, "N", "f@x.co", "r@x.co")
		id, err := c.Send(context.Background(), message())
		srv.Close()
		if err != nil {
			t.Fatalf("status %d: %v", code, err)
		}
		if id != "m-ok" {
			t.Fatalf("status %d: expected m-ok, got %q", code, id)
		}
	}
}

func TestSendMapsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"missing_parameter","message":"to must not be null"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewBrevoClient("k", srv.URL, "N", "f@x.co", "r@x.co")
	_, err := c.Send(context.Background(), message())
	if err == nil {
		t.Fatal("expected an error for a 400 response")
	}

	var be *BrevoError
	if !errors.As(err, &be) {
		t.Fatalf("expected *BrevoError, got %T: %v", err, err)
	}
	if be.Status != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", be.Status)
	}
	if be.Retryable() {
		t.Fatal("a 400 response must not be retryable")
	}
	if !strings.Contains(be.Error(), "400") {
		t.Fatalf("error should carry the status: %v", be)
	}
}

func TestBrevoErrorRetryableMatrix(t *testing.T) {
	cases := []struct {
		status    int
		retryable bool
	}{
		{http.StatusBadRequest, false},
		{http.StatusForbidden, false},
		{http.StatusNotFound, false},
		{http.StatusTooManyRequests, true},
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
		{http.StatusServiceUnavailable, true},
	}
	for _, tc := range cases {
		be := &BrevoError{Status: tc.status, Body: "x"}
		if got := be.Retryable(); got != tc.retryable {
			t.Fatalf("status %d: retryable = %v, want %v", tc.status, got, tc.retryable)
		}
	}
}

func TestSendErrorWhenSuccessBodyIsInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not-json`))
	}))
	t.Cleanup(srv.Close)

	c := NewBrevoClient("k", srv.URL, "N", "f@x.co", "r@x.co")
	_, err := c.Send(context.Background(), message())
	if err == nil || !strings.Contains(err.Error(), "unexpected response body") {
		t.Fatalf("expected unmarshal error, got %v", err)
	}
}

func TestSendRequestFailure(t *testing.T) {
	// A closed server yields a transport error, which must surface as a wrapped
	// error so the worker can retry.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	c := NewBrevoClient("k", srv.URL, "N", "f@x.co", "r@x.co")
	srv.Close()

	_, err := c.Send(context.Background(), message())
	if err == nil || !strings.Contains(err.Error(), "brevo request") {
		t.Fatalf("expected transport error, got %v", err)
	}
}

func TestNoopSenderLogsAndReturnsFixedID(t *testing.T) {
	n := NewNoopSender(nil)
	id, err := n.Send(context.Background(), message())
	if err != nil {
		t.Fatalf("noop send must not fail: %v", err)
	}
	if id != "noop-message-id" {
		t.Fatalf("expected noop-message-id, got %q", id)
	}
}

func TestBaseURLTrailingSlashIsTrimmed(t *testing.T) {
	c := NewBrevoClient("k", "https://api.brevo.com///", "N", "f@x.co", "r@x.co")
	if c.baseURL != "https://api.brevo.com" {
		t.Fatalf("expected trimmed base URL, got %q", c.baseURL)
	}
}
