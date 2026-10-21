package transportgrpc

import (
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/shared"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// toStatus maps an application error to a gRPC status. The mapping mirrors
// how the REST layer interprets errors (Application.AppError in
// internal/api/helpers.go): an AppError carries its own status; a bare
// sentinel escaping a service still has a defined status via shared.StatusFor
// (e.g. repository ErrNotFound → 404, so a well-formed but nonexistent
// resource is codes.NotFound, never a 500); other failures collapse to a
// generic Internal message so raw infrastructure errors never reach the wire.
func toStatus(err error) error {
	if appErr, ok := shared.AsAppError(err); ok {
		if code := toCode(appErr.HTTPStatus); code != codes.Internal {
			return status.Error(code, appErr.Message)
		}
		return status.Error(codes.Internal, "internal error")
	}
	if st := shared.StatusFor(err); st != 500 {
		return status.Error(toCode(st), sentinelMessage(st))
	}
	return status.Error(codes.Internal, "internal error")
}

// sentinelMessage matches the REST layer's sentinel phrasing, so REST and
// gRPC clients see the same text for the same failure.
func sentinelMessage(status int) string {
	if status == 404 {
		return "cannot find resource"
	}
	return "the resource is in a conflicting state"
}

func toCode(httpStatus int) codes.Code {
	switch httpStatus {
	case 400, 422:
		return codes.InvalidArgument
	case 401:
		return codes.Unauthenticated
	case 403:
		return codes.PermissionDenied
	case 404:
		return codes.NotFound
	case 409:
		return codes.FailedPrecondition
	case 410:
		return codes.NotFound
	case 429:
		return codes.ResourceExhausted
	default:
		return codes.Internal
	}
}

// timestamp converts a moment the DB stores in UTC; a zero time maps to nil
// (proto field presence is on the message, not on our pointers).
func timestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func timestampOptional(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamp(*t)
}

func maxAttendees(m *int) *uint32 {
	if m == nil {
		return nil
	}
	v := uint32(*m)
	return &v
}
