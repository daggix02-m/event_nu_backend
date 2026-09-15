package api

import (
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

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
	// A bare sentinel escaping a service (e.g. repository ErrNotFound) still has
	// a defined status: map it here so a well-formed but nonexistent resource
	// returns 404/409 instead of surfacing as an internal 500.
	if status := shared.StatusFor(err); status != http.StatusInternalServerError {
		app.ClientError(w, r, status, sentinelCode(status), sentinelMessage(status))
		return
	}
	app.ServerError(w, r, err)
}

// sentinelCode/Messages pair generic response details with StatusFor's mapped
// statuses. The not-found message intentionally matches Application.NotFound so
// malformed and missing resource ids stay indistinguishable.
func sentinelCode(status int) string {
	if status == http.StatusNotFound {
		return "not_found"
	}
	return "conflict"
}

func sentinelMessage(status int) string {
	if status == http.StatusNotFound {
		return "cannot find resource"
	}
	return "the resource is in a conflicting state"
}

// NotFound writes the standard 404 body. Used when a resource id path
// parameter is malformed or names no resource: the client must not be able to
// distinguish a bad UUID from a missing one (no existence/format hints).
func (app *Application) NotFound(w http.ResponseWriter, r *http.Request) {
	app.ClientError(w, r, http.StatusNotFound, "not_found", "cannot find resource")
}

// ClientError returns a simple error response with the given status and code.
func (app *Application) ClientError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	app.Logger.Warn("client error", "request_id", r.Context().Value(middleware.RequestIDKey), "code", code, "message", message)
	shared.WriteErrorJSON(w, status, code, message)
}
