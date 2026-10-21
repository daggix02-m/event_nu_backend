package transportgrpc

import (
	"errors"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/shared"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestToStatusMapping pins the AppError→gRPC code mapping: each service-level
// HTTP outcome has exactly one gRPC counterpart, and 5xx/non-AppError
// failures collapse to a generic Internal so raw infrastructure errors never
// reach the wire.
func TestToStatusMapping(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		want    codes.Code
		message string
	}{
		{name: "bad request", err: shared.NewAppError("x", "bad things", 400), want: codes.InvalidArgument, message: "bad things"},
		{name: "unprocessable", err: shared.NewAppError("x", "nope", 422), want: codes.InvalidArgument, message: "nope"},
		{name: "unauthenticated", err: shared.NewAppError("x", "who", 401), want: codes.Unauthenticated, message: "who"},
		{name: "permission denied", err: shared.NewAppError("x", "no", 403), want: codes.PermissionDenied, message: "no"},
		{name: "not found", err: shared.NewAppError("x", "gone", 404), want: codes.NotFound, message: "gone"},
		{name: "gone 410", err: shared.NewAppError("x", "gone", 410), want: codes.NotFound, message: "gone"},
		{name: "conflict", err: shared.NewAppError("x", "already", 409), want: codes.FailedPrecondition, message: "already"},
		{name: "rate limit", err: shared.NewAppError("x", "slow down", 429), want: codes.ResourceExhausted, message: "slow down"},
		{name: "server error hides message", err: shared.NewAppError("x", "SQL leak", 500), want: codes.Internal, message: "internal error"},
		{name: "plain error hides message", err: errors.New("boom"), want: codes.Internal, message: "internal error"},
		{name: "bare sentinel → not found", err: shared.ErrNotFound, want: codes.NotFound, message: "cannot find resource"},
		{name: "bare sentinel → conflict", err: shared.ErrConflict, want: codes.FailedPrecondition, message: "the resource is in a conflicting state"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ok := status.FromError(toStatus(tc.err))
			if !ok {
				t.Fatal("expected a status error")
			}
			if s.Code() != tc.want {
				t.Fatalf("code: got %v, want %v", s.Code(), tc.want)
			}
			if s.Message() != tc.message {
				t.Fatalf("message: got %q, want %q (5xx must never leak internals)", s.Message(), tc.message)
			}
		})
	}
}

// TestTimeHelpers pins the protobuf time/int pointer conversions used by the
// event mapping.
func TestTimeHelpers(t *testing.T) {
	if got := timestamp(time.Time{}); got != nil {
		t.Fatalf("zero time: expected nil, got %v", got)
	}
	now := time.Now()
	if got := timestamp(now); got == nil || got.AsTime().Unix() != now.Unix() {
		t.Fatalf("wanted %v, got %v", now, got)
	}
	if got := timestampOptional(nil); got != nil {
		t.Fatalf("nil pointer: expected nil, got %v", got)
	}
	zero := time.Time{}
	if got := timestampOptional(&zero); got != nil {
		t.Fatal("zero pointer: expected nil (zero time maps to absent), got a timestamp")
	}
	if got := maxAttendees(nil); got != nil {
		t.Fatal("nil max_attendees: expected nil")
	}
	twenty := 20
	if got := maxAttendees(&twenty); got == nil || *got != 20 {
		t.Fatalf("wanted 20, got %v", got)
	}
}
