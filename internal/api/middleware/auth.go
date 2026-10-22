package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu_backend/internal/service"
)

const UserIDKey contextKey = "user_id"

func RequireAuth(auth *service.AuthService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, _, ok := parseToken(w, r, auth)
			if !ok {
				return
			}
			ctx := context.WithValue(r.Context(), UserIDKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequireAdmin(auth *service.AuthService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, _, ok := parseToken(w, r, auth)
			if !ok {
				return
			}
			ctx := context.WithValue(r.Context(), UserIDKey, userID)

			// Obtain the request transaction from context (set by
			// BeginRequestTx). Set the RLS context so the downstream DB
			// query runs as this user with the admin role.
			tx, ok := database.TxFromContext(ctx)
			if !ok {
				writeServerError(w)
				return
			}
			if err := database.SetRLSContextTx(ctx, tx, userID, "admin"); err != nil {
				writeServerError(w)
				return
			}

			user, err := auth.GetUser(ctx, userID)
			if err != nil {
				writeServerError(w)
				return
			}
			if user.Role != "admin" {
				writeForbidden(w)
				return
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func OptionalAuth(auth *service.AuthService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if header == "" || !strings.HasPrefix(header, "Bearer ") {
				next.ServeHTTP(w, r)
				return
			}
			token := strings.TrimPrefix(header, "Bearer ")
			userID, _, err := auth.ParseAccessToken(token)
			if err != nil {

				next.ServeHTTP(w, r)
				return
			}
			ctx := context.WithValue(r.Context(), UserIDKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func parseToken(w http.ResponseWriter, r *http.Request, auth *service.AuthService) (string, string, bool) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		writeAuthError(w)
		return "", "", false
	}

	token := strings.TrimPrefix(header, "Bearer ")
	userID, role, err := auth.ParseAccessToken(token)
	if err != nil {
		writeAuthError(w)
		return "", "", false
	}
	return userID, role, true
}

func UserID(ctx context.Context) string {
	id, _ := ctx.Value(UserIDKey).(string)
	return id
}

func writeAuthError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"Authentication required."}}`))
}

func writeForbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"Insufficient permissions."}}`))
}
