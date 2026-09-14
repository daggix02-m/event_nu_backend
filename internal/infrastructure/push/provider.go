// Package push abstracts the mobile push provider. The worker dispatches
// notification records through a Provider (Firebase Cloud Messaging in
// production, a no-op in local dev/tests). The Provider interface exists
// because we need a mock for tests — not an interface-per-struct pattern.
package push

import "context"

// Message is one push notification targeted at a single device token.
type Message struct {
	Token string
	Title string
	Body  string
	// Data is the optional key/value payload delivered next to the message
	// (deep-link target, notification id, …). Values must be strings.
	Data map[string]string
}

// ErrCode is a normalized failure classification, mapped from the provider's
// raw response so the dispatch worker never switches on provider strings.
type ErrCode string

const (
	// CodeInvalidToken means the registration token is gone, malformed, or
	// belongs to a different project. The device must be deregistered, never
	// retried.
	CodeInvalidToken ErrCode = "invalid_token"
	// CodeUnavailable means the provider is temporarily overloaded, down, or
	// rate-limited. The send may be retried with backoff.
	CodeUnavailable ErrCode = "unavailable"
	// CodeFail covers everything else (bad credentials, quota, unexpected
	// payload, …): not retryable, not a token problem.
	CodeFail ErrCode = "fail"
)

// Error is the typed push failure returned by providers.
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string {
	return "push " + string(e.Code) + ": " + e.Msg
}

// IsInvalidToken reports whether the target device should be pruned.
func (e *Error) IsInvalidToken() bool { return e.Code == CodeInvalidToken }

// Retryable reports whether the send may succeed on a later attempt.
func (e *Error) Retryable() bool { return e.Code == CodeUnavailable }

// Provider sends push messages.
type Provider interface {
	// Name is the stable provider key (persisted in config/logs).
	Name() string
	// Send delivers one message and returns the provider's message id.
	Send(ctx context.Context, m Message) (messageID string, err error)
}
