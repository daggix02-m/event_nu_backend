package transportgrpc

import (
	"context"
	"strings"

	"github.com/daggix02-m/event_nu_backend/internal/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type identityKey string

const userIDKey identityKey = "grpc_user_id"

type tokenParser interface {
	ParseAccessToken(token string) (userID, role string, err error)
}

func UserID(ctx context.Context) string {
	id, _ := ctx.Value(userIDKey).(string)
	return id
}

func OptionalAuthInterceptor(auth tokenParser) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if userID, ok := identityFromMD(ctx, auth); ok {
			ctx = context.WithValue(ctx, userIDKey, userID)
		}
		return handler(ctx, req)
	}
}

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
