// Package push abstracts the mobile push provider. The worker dispatches
// notification records through a Provider (Firebase Cloud Messaging in
// production, a no-op in local dev/tests). The Provider interface exists
// because we need a mock for tests — not an interface-per-struct pattern.
package push

import "context"

type Message struct {
	Token string
	Title string
	Body  string

	Data map[string]string
}

type ErrCode string

const (
	CodeInvalidToken ErrCode = "invalid_token"
	// CodeUnavailable means the provider is temporarily overloaded, down, or
	// rate-limited. The send may be retried with backoff.
	CodeUnavailable ErrCode = "unavailable"
	// CodeFail covers everything else (bad credentials, quota, unexpected
	// payload, …): not retryable, not a token problem.
	CodeFail ErrCode = "fail"
)

type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string {
	return "push " + string(e.Code) + ": " + e.Msg
}

func (e *Error) IsInvalidToken() bool { return e.Code == CodeInvalidToken }

func (e *Error) Retryable() bool { return e.Code == CodeUnavailable }

type Provider interface {
	Name() string

	Send(ctx context.Context, m Message) (messageID string, err error)
}
