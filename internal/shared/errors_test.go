package shared

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestAppErrorErrorWithoutCause(t *testing.T) {
	err := NewAppError("bad_request", "invalid input", http.StatusBadRequest)
	if got := err.Error(); got != "invalid input" {
		t.Fatalf("expected bare message, got %q", got)
	}
}

func TestAppErrorErrorWithCause(t *testing.T) {
	err := WrapAppError(errors.New("boom"), "internal", "something broke", http.StatusInternalServerError)
	if got := err.Error(); got != "something broke: boom" {
		t.Fatalf("expected combined message, got %q", got)
	}
}

func TestAppErrorUnwrap(t *testing.T) {
	cause := errors.New("root cause")
	err := WrapAppError(cause, "internal", "wrapped", http.StatusInternalServerError)
	if !errors.Is(err, cause) {
		t.Fatal("expected errors.Is to reach the wrapped cause")
	}
	if got := err.Unwrap(); got != cause {
		t.Fatalf("expected Unwrap to return cause, got %v", got)
	}
}

func TestNewAppErrorSetsFields(t *testing.T) {
	err := NewAppError("conflict", "duplicate", http.StatusConflict)
	if err.Code != "conflict" || err.Message != "duplicate" || err.HTTPStatus != http.StatusConflict {
		t.Fatalf("unexpected AppError fields: %+v", err)
	}
	if err.Err != nil {
		t.Fatalf("expected nil cause, got %v", err.Err)
	}
}

func TestWrapAppErrorSetsFields(t *testing.T) {
	cause := errors.New("cause")
	err := WrapAppError(cause, "code", "msg", 418)
	if err.Code != "code" || err.Message != "msg" || err.HTTPStatus != 418 || err.Err != cause {
		t.Fatalf("unexpected wrapped AppError fields: %+v", err)
	}
}

func TestAsAppErrorDirect(t *testing.T) {
	appErr := NewAppError("x", "y", http.StatusBadRequest)
	got, ok := AsAppError(appErr)
	if !ok || got != appErr {
		t.Fatalf("expected direct *AppError extraction, got %v (ok=%v)", got, ok)
	}
}

func TestAsAppErrorWrapped(t *testing.T) {
	appErr := NewAppError("x", "y", http.StatusBadRequest)
	wrapped := fmt.Errorf("context: %w", appErr)
	got, ok := AsAppError(wrapped)
	if !ok || got != appErr {
		t.Fatalf("expected extraction through fmt %%w wrap, got %v (ok=%v)", got, ok)
	}
}

func TestAsAppErrorMissing(t *testing.T) {
	if got, ok := AsAppError(errors.New("plain")); ok || got != nil {
		t.Fatalf("expected no extraction for plain error, got %v (ok=%v)", got, ok)
	}
}

func TestAsAppErrorNil(t *testing.T) {
	if got, ok := AsAppError(nil); ok || got != nil {
		t.Fatalf("expected no extraction for nil, got %v (ok=%v)", got, ok)
	}
}

func TestStatusFor(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "not found", err: ErrNotFound, want: http.StatusNotFound},
		{name: "conflict", err: ErrConflict, want: http.StatusConflict},
		{name: "wrapped not found", err: fmt.Errorf("lookup: %w", ErrNotFound), want: http.StatusNotFound},
		{name: "wrapped conflict", err: fmt.Errorf("insert: %w", ErrConflict), want: http.StatusConflict},
		{name: "plain error", err: errors.New("boom"), want: http.StatusInternalServerError},
		{name: "nil", err: nil, want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StatusFor(tt.err); got != tt.want {
				t.Fatalf("expected status %d, got %d", tt.want, got)
			}
		})
	}
}

func TestAppErrorNotNullFoundByStatusFor(t *testing.T) {
	// An *AppError is NOT a sentinel, so StatusFor must fall through to 500;
	// handlers route AppErrors through their HTTP status instead.
	err := NewAppError("not_authorized", "nope", http.StatusForbidden)
	if got := StatusFor(err); got != http.StatusInternalServerError {
		t.Fatalf("expected AppError to fall through to 500, got %d", got)
	}
}
