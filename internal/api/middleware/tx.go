package middleware

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

const roleKey contextKey = "role"

// Role returns the RLS role for the request, defaulting to "user".
func Role(ctx context.Context) string {
	if r, ok := ctx.Value(roleKey).(string); ok && r != "" {
		return r
	}
	return "user"
}

// WithRole sets the RLS role for a route group. Public/auth-critical endpoints
// run as 'service' (trusted internal); normal user endpoints default to 'user'.
func WithRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), roleKey, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// BeginRequestTx opens a per-request transaction and commits it after a
// successful handler, rolling back on any error status. Repositories read the
// transaction from the context so every query honors RLS context.
func BeginRequestTx(pool *pgxpool.Pool, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tx, err := pool.Begin(r.Context())
			if err != nil {
				logger.Error("begin request tx", "error", err.Error())
				writeServerError(w)
				return
			}

			rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			ctx := database.ContextWithTx(r.Context(), tx)

			next.ServeHTTP(rec, r.WithContext(ctx))

			if rec.status >= http.StatusBadRequest {
				_ = tx.Rollback(r.Context())
				return
			}
			if err := tx.Commit(r.Context()); err != nil {
				logger.Error("commit request tx", "error", err.Error())
				if !rec.WroteHeader {
					writeServerError(w)
				}
			}
		})
	}
}

// SetRLS applies the request's RLS context (user id + role) to the open
// transaction. Must run inside BeginRequestTx and after RequireAuth.
func SetRLS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tx, ok := database.TxFromContext(r.Context())
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		if err := database.SetRLSContextTx(r.Context(), tx, UserID(r.Context()), Role(r.Context())); err != nil {
			writeServerError(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeServerError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(`{"error":{"code":"internal_error","message":"An unexpected error occurred."}}`))
}