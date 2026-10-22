package transportgrpc

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// LoggingInterceptor logs every unary call's method, duration, and result
// code. Errors are logged once here — no service or handler duplicates it.
func LoggingInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		log.Info("grpc call",
			"method", info.FullMethod,
			"duration", time.Since(start).String(),
			"code", status.Code(err),
		)
		return resp, err
	}
}

// RecoveryInterceptor converts a handler panic into codes.Internal instead
// of letting it kill the server's connection handling. Like the REST
// recoverer it logs the panic without the request payload.
func RecoveryInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("grpc panic recovered", "method", info.FullMethod, "panic", r)
				resp, err = nil, status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}
