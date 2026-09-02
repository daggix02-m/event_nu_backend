package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppEnv string
	Port   string

	DatabaseURL      string
	DatabaseAdminURL string

	JWTSecret          string
	JWTExpiry          time.Duration
	RefreshTokenExpiry time.Duration

	CORSAllowedOrigins []string

	EmailProvider    string
	BrevoAPIKey      string
	BrevoSenderEmail string
	BrevoAPISender   string
	BrevoSenderName  string
	BrevoAPIBase     string
	APIPublicBase    string

	BrevoTemplateWelcome int
	BrevoTemplateVerify  int

	// Worker / email consumer tuning.
	EmailPollInterval   time.Duration
	EmailBatchSize      int
	EmailMaxAttempts    int
	EmailRetryBaseDelay time.Duration
}

// parseDuration supports Go durations ("15m", "1h") plus day suffixes ("30d").
func parseDuration(v string) (time.Duration, error) {
	if strings.HasSuffix(v, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(v, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", v)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(v)
}

// Load reads configuration from the environment and validates it, failing
// fast on missing or invalid values rather than surfacing runtime panics.
func Load() (Config, error) {
	var cfg Config
	var err error

	cfg.AppEnv = getEnv("APP_ENV", "development")
	cfg.Port = getEnv("PORT", "8080")

	cfg.DatabaseURL = getEnv("DATABASE_URL", "")
	cfg.DatabaseAdminURL = getEnv("DATABASE_ADMIN_URL", "")

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("missing required DATABASE_URL")
	}

	cfg.JWTSecret = getEnv("JWT_SECRET", "")
	if cfg.JWTSecret == "" {
		return Config{}, fmt.Errorf("missing required JWT_SECRET")
	}

	if cfg.JWTExpiry, err = parseDuration(getEnv("JWT_EXPIRY", "15m")); err != nil {
		return Config{}, fmt.Errorf("JWT_EXPIRY: %w", err)
	}
	if cfg.RefreshTokenExpiry, err = parseDuration(getEnv("REFRESH_TOKEN_EXPIRY", "30d")); err != nil {
		return Config{}, fmt.Errorf("REFRESH_TOKEN_EXPIRY: %w", err)
	}

	for _, o := range strings.Split(getEnv("CORS_ALLOWED_ORIGINS", ""), ",") {
		if o = strings.TrimSpace(o); o != "" {
			cfg.CORSAllowedOrigins = append(cfg.CORSAllowedOrigins, o)
		}
	}

	cfg.EmailProvider = getEnv("EMAIL_PROVIDER", "noop")
	cfg.BrevoAPIKey = getEnv("BREVO_API_KEY", "")
	cfg.BrevoSenderEmail = getEnv("BREVO_SENDER_EMAIL", "event.nua@gmail.com")
	cfg.BrevoAPISender = getEnv("BREVO_API_SENDER", "")
	cfg.BrevoSenderName = getEnv("BREVO_SENDER_NAME", "Event Nu")
	cfg.BrevoAPIBase = getEnv("BREVO_API_BASE", "https://api.brevo.com")
	cfg.APIPublicBase = getEnv("API_PUBLIC_BASE", "http://localhost:8080")

	if cfg.EmailPollInterval, err = parseDuration(getEnv("EMAIL_POLL_INTERVAL", "2s")); err != nil {
		return Config{}, fmt.Errorf("EMAIL_POLL_INTERVAL: %w", err)
	}
	if cfg.EmailBatchSize, err = strconv.Atoi(getEnv("EMAIL_BATCH_SIZE", "20")); err != nil {
		return Config{}, fmt.Errorf("EMAIL_BATCH_SIZE must be an integer: %w", err)
	}
	if cfg.EmailMaxAttempts, err = strconv.Atoi(getEnv("EMAIL_MAX_ATTEMPTS", "3")); err != nil {
		return Config{}, fmt.Errorf("EMAIL_MAX_ATTEMPTS must be an integer: %w", err)
	}
	if cfg.EmailRetryBaseDelay, err = parseDuration(getEnv("EMAIL_RETRY_BASE_DELAY", "30s")); err != nil {
		return Config{}, fmt.Errorf("EMAIL_RETRY_BASE_DELAY: %w", err)
	}

	if cfg.BrevoTemplateWelcome, err = strconv.Atoi(getEnv("BREVO_TEMPLATE_WELCOME", "0")); err != nil {
		return Config{}, fmt.Errorf("BREVO_TEMPLATE_WELCOME must be an integer: %w", err)
	}
	if cfg.BrevoTemplateVerify, err = strconv.Atoi(getEnv("BREVO_TEMPLATE_VERIFY", "0")); err != nil {
		return Config{}, fmt.Errorf("BREVO_TEMPLATE_VERIFY must be an integer: %w", err)
	}

	if cfg.EmailProvider == "brevo" && cfg.BrevoAPIKey == "" {
		return Config{}, fmt.Errorf("EMAIL_PROVIDER=brevo requires BREVO_API_KEY")
	}
	if cfg.EmailProvider == "brevo" && cfg.BrevoAPISender == "" {
		return Config{}, fmt.Errorf("EMAIL_PROVIDER=brevo requires BREVO_API_SENDER")
	}
	if cfg.EmailProvider == "brevo" && cfg.BrevoTemplateWelcome <= 0 {
		return Config{}, fmt.Errorf("EMAIL_PROVIDER=brevo requires BREVO_TEMPLATE_WELCOME > 0")
	}
	if cfg.EmailProvider == "brevo" && cfg.BrevoTemplateVerify <= 0 {
		return Config{}, fmt.Errorf("EMAIL_PROVIDER=brevo requires BREVO_TEMPLATE_VERIFY > 0")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
