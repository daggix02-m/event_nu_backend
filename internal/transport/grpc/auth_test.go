package transportgrpc

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// stubParser scripts ParseAccessToken outcomes for the interceptor table
// test, keeping the hermetic suite independent of JWT machinery.
type stubParser struct {
	out  map[string]struct{ userID, role string }
	fail map[string]bool
}

func (s *stubParser) ParseAccessToken(token string) (string, string, error) {
	if s.fail[token] {
		return "", "", fmt.Errorf("bad token")
	}
	if v, ok := s.out[token]; ok {
		return v.userID, v.role, nil
	}
	return "", "", fmt.Errorf("unknown token")
}

func incoming(ctx context.Context, authHeader string) context.Context {
	md := metadata.MD{}
	if authHeader != "" {
		md.Set("authorization", authHeader)
	}
	return metadata.NewIncomingContext(ctx, md)
}

func handlerCaptureID(ctx context.Context, _ any) (any, error) {
	return UserID(ctx), nil // whatever the handler returns, the identity rides the response
}

// TestOptionalAuthInterceptor pins the REST OptionalAuth contract: a valid
// Bearer token yields the identity; no/malformed/invalid tokens are all
// anonymous — the interceptor never rejects on its own.
func TestOptionalAuthInterceptor(t *testing.T) {
	parser := &stubParser{
		out:  map[string]struct{ userID, role string }{"good": {userID: "user-1", role: "user"}},
		fail: map[string]bool{"expired": true},
	}
	inter := OptionalAuthInterceptor(parser)
	handler := func(ctx context.Context, _ any) (any, error) { return UserID(ctx), nil }

	cases := []struct {
		name   string
		md     context.Context
		wantID string
	}{
		{name: "no metadata at all", md: context.Background(), wantID: ""},
		{name: "empty authorization header", md: incoming(context.Background(), ""), wantID: ""},
		{name: "valid bearer token", md: incoming(context.Background(), "Bearer good"), wantID: "user-1"},
		{name: "garbage token treated anonymous", md: incoming(context.Background(), "Bearer garbage"), wantID: ""},
		{name: "expired token treated anonymous", md: incoming(context.Background(), "Bearer expired"), wantID: ""},
		{name: "not bearer scheme", md: incoming(context.Background(), "Basic good"), wantID: ""},
		{name: "bearer without space", md: incoming(context.Background(), "Bearergood"), wantID: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := inter(tc.md, nil, &grpc.UnaryServerInfo{FullMethod: "/eventnu.v1.EventService/GetEvent"}, handler)
			if err != nil || resp != tc.wantID {
				t.Fatalf("got (%v,%v), want (%q,nil)", resp, err, tc.wantID)
			}
		})
	}
}

// TestOptionalAuthNilServiceGuard documents the hermetic wiring: no auth
// service configured means every caller is anonymous even with a token.
func TestOptionalAuthNilServiceGuard(t *testing.T) {
	inter := OptionalAuthInterceptor(nil)
	resp, err := inter(incoming(context.Background(), "Bearer good"), nil, &grpc.UnaryServerInfo{FullMethod: "m"}, handlerCaptureID)
	if err != nil || resp != "" {
		t.Fatalf("nil auth service: got (%v,%v), want anonymous", resp, err)
	}
}

// TestUserIDUnrelatedContexts pins that gRPC and REST contexts stay separate:
// the REST middleware's key would be a different concrete type, so a gRPC
// identity cannot collide with an HTTP one even if a context got reused.
func TestUserIDUnrelatedContexts(t *testing.T) {
	reused := context.WithValue(context.Background(), userIDKey, "from-grpc")
	if got := UserID(reused); got != "from-grpc" {
		t.Fatalf("own key: got %q", got)
	}
	// A context key of a *different* type carrying "" is invisible here.
	other := context.WithValue(context.Background(), "grpc_user_id", "x")
	if got := UserID(other); got != "" {
		t.Fatalf("string key must not match typed key: got %q", got)
	}
}
