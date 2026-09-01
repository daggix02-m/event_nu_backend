package api

import (
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// ServerError logs the underlying error and returns a generic 500 — internal
// details never reach the client.
func (app *Application) ServerError(w http.ResponseWriter, r *http.Request, err error) {
	app.Logger.Error("server error", "request_id", r.Context().Value(middleware.RequestIDKey), "method", r.Method, "path", r.URL.Path, "error", err.Error())
	shared.WriteErrorJSON(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
}

// AppError maps an *shared.AppError (or a wrapped sentinel) to its HTTP response.
func (app *Application) AppError(w http.ResponseWriter, r *http.Request, err error) {
	if appErr, ok := shared.AsAppError(err); ok {
		app.Logger.Warn("request error", "request_id", r.Context().Value(middleware.RequestIDKey), "code", appErr.Code, "error", err.Error())
		shared.WriteErrorJSON(w, appErr.HTTPStatus, appErr.Code, appErr.Message)
		return
	}
	app.ServerError(w, r, err)
}

// ClientError returns a simple error response with the given status and code.
func (app *Application) ClientError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	app.Logger.Warn("client error", "request_id", r.Context().Value(middleware.RequestIDKey), "code", code, "message", message)
	shared.WriteErrorJSON(w, status, code, message)
}