package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/repository"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func emailTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		dir, _ := os.Getwd()
		for {
			p := filepath.Join(dir, ".env")
			if _, err := os.Stat(p); err == nil {
				_ = godotenv.Load(p)
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping email integration tests")
	}
	// Verification/redemption flows run under the trusted 'service' role.
	pool, err := database.NewPoolWithRole(context.Background(), dsn, "service")
	if err != nil {
		t.Skipf("database unreachable (%v)", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func emailService(t *testing.T, pool *pgxpool.Pool, templates map[string]int) *EmailService {
	t.Helper()
	// Isolation: purge rows left by prior runs against the shared dev DB.
	for _, tbl := range []string{"email_logs", "email_outbox", "email_codes"} {
		if _, err := pool.Exec(context.Background(), "DELETE FROM "+tbl); err != nil {
			t.Fatalf("purge %s: %v", tbl, err)
		}
	}
	cfg := config.Config{
		APIPublicBase:        "http://localhost:8080",
		EmailMaxAttempts:     3,
		BrevoTemplateWelcome: templates["welcome"],
		BrevoTemplateVerify:  templates["verify"],
	}
	users := repository.NewUserRepository(pool)
	return NewEmailService(
		repository.NewOutboxRepository(pool),
		repository.NewEmailCodeRepository(pool),
		users,
		cfg,
	)
}

func createTestUser(t *testing.T, pool *pgxpool.Pool) *domain.User {
	t.Helper()
	email := fmt.Sprintf("email+%d@test.example", time.Now().UnixNano())
	user, err := repository.NewUserRepository(pool).Create(context.Background(), email, "x", "user"+fmt.Sprint(time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func TestVerifyCodeFlow(t *testing.T) {
	pool := emailTestPool(t)
	svc := emailService(t, pool, map[string]int{"welcome": 10, "verify": 11})
	user := createTestUser(t, pool)

	code, err := svc.SendVerification(context.Background(), user)
	if err != nil {
		t.Fatalf("send verification: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("expected 6-digit code, got %q", code)
	}

	// Outbox must contain the verification email with a code-free deep link
	// and the 6-digit code as a separate param.
	var count int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM email_outbox WHERE recipient_email = $1 AND template_id = 11`, user.Email).Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 verification outbox row, got %d", count)
	}

	var verifyURL, verifyCode string
	if err := pool.QueryRow(context.Background(),
		`SELECT params->>'verify_url', params->>'verify_code' FROM email_outbox WHERE recipient_email = $1 AND template_id = 11`,
		user.Email).Scan(&verifyURL, &verifyCode); err != nil {
		t.Fatalf("read outbox params: %v", err)
	}
	if verifyURL != "eventnu://verify" {
		t.Fatalf("expected eventnu://verify deep link, got %q", verifyURL)
	}
	if verifyCode != code {
		t.Fatalf("expected verify_code %q in outbox, got %q", code, verifyCode)
	}
	if strings.Contains(verifyURL, code) {
		t.Fatal("verification code must never appear in the emitted URL")
	}

	// Valid code verifies the user.
	if err := svc.VerifyCode(context.Background(), code); err != nil {
		t.Fatalf("verify code: %v", err)
	}
	var verified bool
	_ = pool.QueryRow(context.Background(),
		`SELECT is_verified FROM users WHERE id = $1`, user.ID).Scan(&verified)
	if !verified {
		t.Fatal("expected user to be verified")
	}

	// Reusing the code fails.
	if err := svc.VerifyCode(context.Background(), code); err == nil {
		t.Fatal("expected replay rejection")
	}
}

func TestVerifyCodeRejectsBadCode(t *testing.T) {
	pool := emailTestPool(t)
	svc := emailService(t, pool, map[string]int{"welcome": 10, "verify": 11})
	user := createTestUser(t, pool)
	_, _ = svc.SendVerification(context.Background(), user)

	if err := svc.VerifyCode(context.Background(), "000000"); err == nil {
		t.Fatal("expected invalid code rejection")
	}
	if err := svc.VerifyCode(context.Background(), ""); err == nil {
		t.Fatal("expected empty code rejection")
	}
}

func TestEnqueueSkipsUnconfiguredTemplate(t *testing.T) {
	pool := emailTestPool(t)
	svc := emailService(t, pool, map[string]int{"welcome": 0, "verify": 0})
	user := createTestUser(t, pool)

	// No template IDs configured → no outbox rows, no error.
	if err := svc.SendWelcome(context.Background(), user); err != nil {
		t.Fatalf("welcome: %v", err)
	}
	if _, err := svc.SendVerification(context.Background(), user); err != nil {
		t.Fatalf("verification: %v", err)
	}

	var count int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM email_outbox WHERE recipient_email = $1`, user.Email).Scan(&count)
	if count != 0 {
		t.Fatalf("expected 0 outbox rows when templates unconfigured, got %d", count)
	}
}
