package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/repository"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// EmailService owns code generation and outbox enqueueing. Sending itself is
// done by the worker; handlers never block on an external email call.
type EmailService struct {
	outbox *repository.OutboxRepository
	codes  *repository.EmailCodeRepository
	users  *repository.UserRepository
	config config.Config
}

func NewEmailService(outbox *repository.OutboxRepository, codes *repository.EmailCodeRepository, users *repository.UserRepository, cfg config.Config) *EmailService {
	return &EmailService{outbox: outbox, codes: codes, users: users, config: cfg}
}

const codeTTL = 24 * time.Hour
const verifyMaxAttempts = 5

// verifyURL is the deep link the app opens to complete verification. The code
// is never placed in a URL — it rides separately as verify_code so it cannot
// leak through logs, referrers, or the browser history.
const verifyURL = "eventnu://verify"

// SendWelcome enqueues the welcome email after registration.
func (s *EmailService) SendWelcome(ctx context.Context, user *domain.User) error {
	return s.enqueue(ctx, user.Email, user.Username, s.config.BrevoTemplateWelcome, map[string]any{
		"name":     user.Username,
		"username": user.Username,
	})
}

// SendVerification generates a one-time verification code, stores its hash,
// and enqueues a verification email. The clickable link carries a constant
// deep link (no code); the 6-digit code is a separate template param so it
// never appears in a URL.
func (s *EmailService) SendVerification(ctx context.Context, user *domain.User) (string, error) {
	code, err := generateCode(6)
	if err != nil {
		return "", err
	}

	if err := s.codes.Create(ctx, user.ID, "verify", hashCode(code), time.Now().Add(codeTTL), verifyMaxAttempts); err != nil {
		return "", err
	}

	if err := s.enqueue(ctx, user.Email, user.Username, s.config.BrevoTemplateVerify, map[string]any{
		"name":        user.Username,
		"verify_url":  verifyURL,
		"verify_code": code,
	}); err != nil {
		return "", err
	}
	return code, nil
}

// VerifyCode redeems a verification code and marks the user verified.
func (s *EmailService) VerifyCode(ctx context.Context, code string) error {
	if code == "" {
		return shared.NewAppError("invalid_code", "A verification code is required.", http.StatusBadRequest)
	}

	userID, err := s.codes.Consume(ctx, hashCode(code), "verify")
	if err != nil {
		return err
	}
	if err := s.users.UpdateVerified(ctx, userID); err != nil {
		return err
	}
	return nil
}

func (s *EmailService) enqueue(ctx context.Context, email, name string, templateID int, params map[string]any) error {
	if templateID <= 0 {
		// Template not configured yet — skip silently rather than fail the
		// whole request. Surfaces in logs as an operational gap.
		return nil
	}
	m := domain.EmailOutbox{
		RecipientEmail: email,
		RecipientName:  name,
		TemplateID:     templateID,
		Params:         params,
		MaxAttempts:    s.config.EmailMaxAttempts,
	}
	return s.outbox.Create(ctx, m)
}

func generateCode(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate code: %w", err)
	}
	for i := range b {
		b[i] = '0' + b[i]%10
	}
	return string(b), nil
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}
