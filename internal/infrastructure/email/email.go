package email

import "context"

// Message is a transactional email rendered by a Brevo dashboard template.
type Message struct {
	RecipientName string
	RecipientEmail string
	TemplateID    int
	Params        map[string]any
}

// Sender sends a templated transactional email. The interface exists because
// we need a mock/fake for tests — not as an interface-per-struct pattern.
type Sender interface {
	Send(ctx context.Context, m Message) (messageID string, err error)
}