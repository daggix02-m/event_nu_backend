package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/daggix02-m/event_nu/backend/internal/service"
)

const UserIDKey contextKey = "user_id"

// RequireAuth validates the Bearer access token and injects the user ID into
// the request context. Apply only to protected route groups.
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

// RequireAdmin validates the Bearer token and requires the admin role.
// Admin business actions stay in Go (they trigger side effects + audits).
func RequireAdmin(auth *service.AuthService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, role, ok := parseToken(w, r, auth)
			if !ok {
				return
			}
			if role != "admin" {
				writeForbidden(w)
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

// UserID returns the authenticated user ID from the context, or "".
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