package shared

import (
	"errors"
	"fmt"
	"net/http"
)

// AppError is the canonical application error. Services return it so handlers
// can map it to an HTTP response without ad-hoc per-handler logic.
type AppError struct {
	Code       string
	Message    string
	HTTPStatus int
	Err        error
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *AppError) Unwrap() error { return e.Err }

func NewAppError(code, message string, status int) *AppError {
	return &AppError{Code: code, Message: message, HTTPStatus: status}
}

func WrapAppError(err error, code, message string, status int) *AppError {
	return &AppError{Code: code, Message: message, HTTPStatus: status, Err: err}
}

// AsAppError extracts an *AppError from err, if present.
func AsAppError(err error) (*AppError, bool) {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr, true
	}
	return nil, false
}

// Common sentinel errors surfaced by repositories/services.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// HTTP status mapping used when a sentinel error escapes a service.
func StatusFor(err error) int {
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}