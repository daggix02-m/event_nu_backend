package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type BrevoClient struct {
	apiKey       string
	baseURL      string
	senderName   string
	senderEmail  string
	replyToEmail string
	httpClient   *http.Client
}

func NewBrevoClient(apiKey, baseURL, senderName, senderEmail, replyToEmail string) *BrevoClient {
	return &BrevoClient{
		apiKey:       apiKey,
		baseURL:      strings.TrimRight(baseURL, "/"),
		senderName:   senderName,
		senderEmail:  senderEmail,
		replyToEmail: replyToEmail,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

type sendPayload struct {
	Sender     senderRef   `json:"sender"`
	ReplyTo    senderRef   `json:"replyTo,omitempty"`
	To         []senderRef `json:"to"`
	TemplateID int         `json:"templateId"`
	Params     any         `json:"params,omitempty"`
}

type senderRef struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type sendResponse struct {
	MessageID string `json:"messageId"`
}

// Send posts one transactional email. The caller (worker) owns retry/backoff.
func (c *BrevoClient) Send(ctx context.Context, m Message) (string, error) {
	payload := sendPayload{
		Sender:     senderRef{Name: c.senderName, Email: c.senderEmail},
		ReplyTo:    senderRef{Name: c.senderName, Email: c.replyToEmail},
		To:         []senderRef{{Name: m.RecipientName, Email: m.RecipientEmail}},
		TemplateID: m.TemplateID,
		Params:     m.Params,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal brevo payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v3/smtp/email", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build brevo request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("brevo request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
		var sr sendResponse
		if err := json.Unmarshal(respBody, &sr); err != nil {
			return "", fmt.Errorf("brevo unexpected response body: %w", err)
		}
		return sr.MessageID, nil
	}

	return "", &BrevoError{Status: resp.StatusCode, Body: strings.TrimSpace(string(respBody))}
}

type BrevoError struct {
	Status int
	Body   string
}

func (e *BrevoError) Error() string {
	return fmt.Sprintf("brevo api returned %d: %s", e.Status, e.Body)
}

func (e *BrevoError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}
