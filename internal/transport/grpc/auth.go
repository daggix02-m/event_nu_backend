package transportgrpc

import (
	"context"
	"strings"

	"github.com/daggix02-m/event_nu_backend/internal/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// contextKey aliases package-private string keys, exactly as the REST
// middleware package does with UserIDKey. The two packages keep separate
// key types on purpose: a gRPC context must never be confused with an HTTP
// context from another transport.
type identityKey string

const userIDKey identityKey = "grpc_user_id"

// tokenParser is the consumer-side view of AuthService the gRPC transport
// needs (interface segregation: one method).
type tokenParser interface {
	ParseAccessToken(token string) (userID, role string, err error)
}

// UserID returns the authenticated user ID from a gRPC call's context, or ""
// for anonymous calls. Same contract as middleware.UserID on the REST side.
func UserID(ctx context.Context) string {
	id, _ := ctx.Value(userIDKey).(string)
	return id
}

// OptionalAuthInterceptor mirrors the REST middleware's OptionalAuth: a
// well-formed Bearer token adds the caller's identity to the context; a
// missing, malformed, or invalid token is simply anonymous — optional-auth
// routes never reject. (Requiring auth on a method stays a per-service
// decision for the slice that introduces a protected RPC.)
func OptionalAuthInterceptor(auth tokenParser) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if userID, ok := identityFromMD(ctx, auth); ok {
			ctx = context.WithValue(ctx, userIDKey, userID)
		}
		return handler(ctx, req)
	}
}

// identityFromMD extracts the Bearer token from incoming metadata and parses
// it. Metadata keys are lowercase-normalized by gRPC, so "authorization"
// matches an HTTP "Authorization" header. Ok is false when the caller is
// anonymous (no user id) — the same three cases as REST's OptionalAuth.
func identityFromMD(ctx context.Context, auth tokenParser) (string, bool) {
	if auth == nil {
		return "", false
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", false
	}
	values := md.Get("authorization")
	if len(values) == 0 {
		return "", false
	}
	header := values[0]
	if !strings.HasPrefix(header, "Bearer ") {
		return "", false
	}
	userID, _, err := auth.ParseAccessToken(strings.TrimPrefix(header, "Bearer "))
	if err != nil {
		return "", false
	}
	return userID, true
}

// Compile-time proof that the gRPC transport can wrap the real AuthService.
var _ tokenParser = (*service.AuthService)(nil)
