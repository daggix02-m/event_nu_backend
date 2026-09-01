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
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				writeAuthError(w)
				return
			}

			token := strings.TrimPrefix(header, "Bearer ")
			userID, err := auth.ParseAccessToken(token)
			if err != nil {
				writeAuthError(w)
				return
			}

			ctx := context.WithValue(r.Context(), UserIDKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
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