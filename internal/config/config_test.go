package config

import (
	"strings"
	"testing"
)

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	t.Cleanup(func() {
		for k := range kv {
			t.Setenv(k, "")
		}
	})
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL": "",
		"JWT_SECRET":   "secret",
	})
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("expected DATABASE_URL error, got %v", err)
	}
}

func TestLoadRequiresJWTSecret(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL": "postgres://localhost/db",
		"JWT_SECRET":   "",
	})
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("expected JWT_SECRET error, got %v", err)
	}
}

func TestLoadBrevoProviderRequiresKey(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":   "postgres://localhost/db",
		"JWT_SECRET":     "secret",
		"EMAIL_PROVIDER": "brevo",
		"BREVO_API_KEY":  "",
		"BREVO_API_SENDER": "sender@example.com",
	})
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "BREVO_API_KEY") {
		t.Fatalf("expected BREVO_API_KEY error, got %v", err)
	}
}

func TestLoadValid(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":        "postgres://localhost/db",
		"JWT_SECRET":          "secret",
		"EMAIL_PROVIDER":      "noop",
		"BREVO_TEMPLATE_WELCOME": "12",
		"JWT_EXPIRY":          "15m",
		"REFRESH_TOKEN_EXPIRY": "30d",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BrevoTemplateWelcome != 12 {
		t.Fatalf("expected template welcome 12, got %d", cfg.BrevoTemplateWelcome)
	}
	if cfg.RefreshTokenExpiry.Hours() != 24*30 {
		t.Fatalf("expected 30 days refresh expiry, got %v", cfg.RefreshTokenExpiry)
	}
	if cfg.Port != "8080" {
		t.Fatalf("expected default port 8080, got %q", cfg.Port)
	}
}