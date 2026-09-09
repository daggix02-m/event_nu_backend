package config

import (
	"strings"
	"testing"
	"time"
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
		"DATABASE_URL":     "postgres://localhost/db",
		"JWT_SECRET":       "secret",
		"EMAIL_PROVIDER":   "brevo",
		"BREVO_API_KEY":    "",
		"BREVO_API_SENDER": "sender@example.com",
	})
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "BREVO_API_KEY") {
		t.Fatalf("expected BREVO_API_KEY error, got %v", err)
	}
}

func TestLoadBrevoProviderRequiresSender(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":     "postgres://localhost/db",
		"JWT_SECRET":       "secret",
		"EMAIL_PROVIDER":   "brevo",
		"BREVO_API_KEY":    "key",
		"BREVO_API_SENDER": "", // must be cleared: .env may set it
	})
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "BREVO_API_SENDER") {
		t.Fatalf("expected BREVO_API_SENDER error, got %v", err)
	}
}

func TestLoadRejectsInvalidTemplateInt(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":           "postgres://localhost/db",
		"JWT_SECRET":             "secret",
		"EMAIL_PROVIDER":         "noop",
		"BREVO_TEMPLATE_WELCOME": "not-a-number",
	})
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "BREVO_TEMPLATE_WELCOME") {
		t.Fatalf("expected BREVO_TEMPLATE_WELCOME error, got %v", err)
	}
}

func TestLoadRejectsInvalidJWTExpiry(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL": "postgres://localhost/db",
		"JWT_SECRET":   "secret",
		"JWT_EXPIRY":   "soon",
	})
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "JWT_EXPIRY") {
		t.Fatalf("expected JWT_EXPIRY error, got %v", err)
	}
}

func TestLoadRejectsInvalidRefreshExpiry(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":         "postgres://localhost/db",
		"JWT_SECRET":           "secret",
		"REFRESH_TOKEN_EXPIRY": "not-a-duration",
	})
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "REFRESH_TOKEN_EXPIRY") {
		t.Fatalf("expected REFRESH_TOKEN_EXPIRY error, got %v", err)
	}
}

func TestLoadRejectsInvalidEmailPollInterval(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":        "postgres://localhost/db",
		"JWT_SECRET":          "secret",
		"EMAIL_POLL_INTERVAL": "fast",
	})
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "EMAIL_POLL_INTERVAL") {
		t.Fatalf("expected EMAIL_POLL_INTERVAL error, got %v", err)
	}
}

func TestLoadParsesCORSAllowedOrigins(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":         "postgres://localhost/db",
		"JWT_SECRET":           "secret",
		"CORS_ALLOWED_ORIGINS": "https://app.example.com, https://admin.example.com ,",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"https://app.example.com", "https://admin.example.com"}
	if len(cfg.CORSAllowedOrigins) != len(want) {
		t.Fatalf("expected %d origins, got %v", len(want), cfg.CORSAllowedOrigins)
	}
	for i := range want {
		if cfg.CORSAllowedOrigins[i] != want[i] {
			t.Fatalf("origin[%d] = %q, want %q", i, cfg.CORSAllowedOrigins[i], want[i])
		}
	}
}

func TestLoadBrevoRequiresTemplates(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		welcome  string
		verify   string
		wantErr  string
	}{
		{name: "missing both", provider: "brevo", wantErr: "BREVO_TEMPLATE_WELCOME"},
		{name: "missing verify", provider: "brevo", welcome: "3", wantErr: "BREVO_TEMPLATE_VERIFY"},
		{name: "all set ok", provider: "brevo", welcome: "3", verify: "4"},
		{name: "noop ignores missing templates", provider: "noop"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, map[string]string{
				"DATABASE_URL":           "postgres://localhost/db",
				"JWT_SECRET":             "secret",
				"EMAIL_PROVIDER":         tt.provider,
				"BREVO_API_KEY":          "key",
				"BREVO_API_SENDER":       "sender@example.com",
				"BREVO_TEMPLATE_WELCOME": tt.welcome,
				"BREVO_TEMPLATE_VERIFY":  tt.verify,
			})
			_, err := Load()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestLoadValid(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":           "postgres://localhost/db",
		"JWT_SECRET":             "secret",
		"EMAIL_PROVIDER":         "noop",
		"BREVO_TEMPLATE_WELCOME": "12",
		"JWT_EXPIRY":             "15m",
		"REFRESH_TOKEN_EXPIRY":   "30d",
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

func TestLoadAuthRateLimitDefaults(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL": "postgres://localhost/db",
		"JWT_SECRET":   "secret",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []struct {
		name   string
		limit  int
		window string
	}{
		{"login", 20, "5m"},
		{"register", 5, "1h"},
		{"refresh", 30, "5m"},
		{"verify", 20, "1h"},
	}
	got := []struct {
		limit  int
		window time.Duration
	}{
		{cfg.AuthLoginLimit, cfg.AuthLoginWindow},
		{cfg.AuthRegisterLimit, cfg.AuthRegisterWindow},
		{cfg.AuthRefreshLimit, cfg.AuthRefreshWindow},
		{cfg.AuthVerifyLimit, cfg.AuthVerifyWindow},
	}
	for i := range want {
		if got[i].limit != want[i].limit {
			t.Fatalf("%s limit: expected %d, got %d", want[i].name, want[i].limit, got[i].limit)
		}
		wantWindow := mustDuration(t, want[i].window)
		if got[i].window != wantWindow {
			t.Fatalf("%s window: expected %v, got %v", want[i].name, wantWindow, got[i].window)
		}
	}
}

func TestLoadRejectsInvalidRateLimits(t *testing.T) {
	tests := []struct {
		name   string
		limit  string
		window string
		want   string
	}{
		{name: "zero limit", limit: "0", window: "5m", want: "AUTH_LOGIN_RATE_LIMIT must be > 0"},
		{name: "negative limit", limit: "-1", window: "5m", want: "AUTH_LOGIN_RATE_LIMIT must be > 0"},
		{name: "non-integer limit", limit: "abc", window: "5m", want: "AUTH_LOGIN_RATE_LIMIT must be an integer"},
		{name: "zero window", limit: "20", window: "0s", want: "AUTH_LOGIN_RATE_WINDOW must be > 0"},
		{name: "invalid window", limit: "20", window: "soon", want: "AUTH_LOGIN_RATE_WINDOW"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, map[string]string{
				"DATABASE_URL":              "postgres://localhost/db",
				"JWT_SECRET":                "secret",
				"AUTH_LOGIN_RATE_LIMIT":     tt.limit,
				"AUTH_LOGIN_RATE_WINDOW":    tt.window,
				"AUTH_REGISTER_RATE_LIMIT":  "5",
				"AUTH_REGISTER_RATE_WINDOW": "1h",
				"AUTH_REFRESH_RATE_LIMIT":   "30",
				"AUTH_REFRESH_RATE_WINDOW":  "5m",
				"AUTH_VERIFY_RATE_LIMIT":    "20",
				"AUTH_VERIFY_RATE_WINDOW":   "1h",
			})
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func mustDuration(t *testing.T, s string) time.Duration {
	t.Helper()
	d, err := time.ParseDuration(s)
	if err != nil {
		t.Fatalf("bad test duration %q: %v", s, err)
	}
	return d
}
