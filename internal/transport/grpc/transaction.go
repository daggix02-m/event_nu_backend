package transportgrpc

import (
	"context"
	"log/slog"

	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/database"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RoleFor names the RLS role an RPC runs as. The EventServer maps every
// method to "user" to mirror the REST routes they echo: the public catalog
// reads run anonymous-but-scoped (row-content visibility), the protected
// ListMyEvents runs with the caller's identity. Other servers pass their own
// mapping (e.g. worker-context methods may claim "service").
type RoleFor func(fullMethod string) string

// RoleAlways pins a single role for the whole server.
func RoleAlways(role string) RoleFor {
	return func(string) string { return role }
}

// TransactionInterceptor is the gRPC mirror of the REST request pipeline's
// BeginRequestTx + SetRLS pairing: every RPC runs inside one transaction;
// the transaction carries the RLS context (caller identity from the auth
// interceptor + the mapped role) so row-level policies evaluate exactly as
// they do on the HTTP side. Handler errors roll back; success commits.
func TransactionInterceptor(pool *pgxpool.Pool, roleFor RoleFor, logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			if logger != nil {
				logger.Error("grpc request tx begin", "method", info.FullMethod, "error", err.Error())
			}
			return nil, status.Error(codes.Unavailable, "database unavailable")
		}
		tctx := database.ContextWithTx(ctx, tx)
		if err := database.SetRLSContextTx(tctx, tx, UserID(tctx), roleFor(info.FullMethod)); err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			if logger != nil {
				logger.Error("grpc set RLS context", "method", info.FullMethod, "error", err.Error())
			}
			return nil, status.Error(codes.Internal, "internal error")
		}

		resp, err := handler(tctx, req)
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			if logger != nil {
				logger.Error("grpc handler error", "method", info.FullMethod, "error", err.Error())
			}
			return nil, err
		}
		if err := tx.Commit(context.WithoutCancel(ctx)); err != nil {
			if logger != nil {
				logger.Error("grpc request tx commit", "method", info.FullMethod, "error", err.Error())
			}
			return nil, status.Error(codes.Internal, "internal error")
		}
		return resp, nil
	}
}
