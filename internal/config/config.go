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

	GRPCAddr string

	KafkaBrokers []string

	KafkaTopicEvents string

	KafkaPushGroup string

	DatabaseURL      string
	DatabaseAdminURL string

	JWTSecret          string
	JWTExpiry          time.Duration
	RefreshTokenExpiry time.Duration

	// TicketQRSecret signs ticket check-in QR payloads (HMAC-SHA256). A
	// dedicated secret is best; it falls back to JWTSecret when unset so the
	// gate/dev environments work without extra configuration.
	TicketQRSecret string

	PaymentProvider    string
	ChapaSecretKey     string
	ChapaAPIBase       string
	ChapaWebhookSecret string
	ChapaRedirectBase  string

	SyncMaxLimit int

	PushProvider      string
	FCMServiceAccount string
	FCMProjectID      string
	FCMAPIBase        string
	FCMTokenURL       string

	PushPollInterval   time.Duration
	PushBatchSize      int
	PushMaxAttempts    int
	PushRetryBaseDelay time.Duration

	CORSAllowedOrigins []string

	// Authentication rate limits. In-memory fixed-window limiters — the API is
	// a single instance, so this is intentional (see middleware/ratelimit.go).
	// Each limit/window pair guards both the IP bucket and, for login and
	// register, the account bucket.
	AuthLoginLimit     int
	AuthLoginWindow    time.Duration
	AuthRegisterLimit  int
	AuthRegisterWindow time.Duration
	AuthRefreshLimit   int
	AuthRefreshWindow  time.Duration
	AuthVerifyLimit    int
	AuthVerifyWindow   time.Duration

	EmailProvider    string
	BrevoAPIKey      string
	BrevoSenderEmail string
	BrevoAPISender   string
	BrevoSenderName  string
	BrevoAPIBase     string
	APIPublicBase    string

	BrevoTemplateWelcome int
	BrevoTemplateVerify  int

	EmailPollInterval time.Duration

	// DomainEventPollInterval paces the outbox relay when Kafka is enabled.
	DomainEventPollInterval time.Duration
	EmailBatchSize          int
	EmailMaxAttempts        int
	EmailRetryBaseDelay     time.Duration

	MediaStorageProvider   string
	MediaS3Endpoint        string
	MediaS3Region          string
	MediaS3Bucket          string
	MediaS3AccessKey       string
	MediaS3SecretKey       string
	MediaPublicBase        string
	MediaLocalDir          string
	MediaUploadExpiry      time.Duration
	MediaMaxUploadBytes    int64
	MediaJobPollInterval   time.Duration
	MediaJobBatchSize      int
	MediaJobMaxAttempts    int
	MediaJobRetryBaseDelay time.Duration

	AnalyticsIngestRateLimit    int
	AnalyticsIngestRateWindow   time.Duration
	AnalyticsMaxBatch           int
	AnalyticsMaxPropertiesBytes int
	AnalyticsRetentionDays      int
	AnalyticsRollupPollInterval time.Duration
}

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

func Load() (Config, error) {
	var cfg Config
	var err error

	cfg.AppEnv = getEnv("APP_ENV", "development")
	cfg.Port = getEnv("PORT", "8080")
	cfg.GRPCAddr = getEnv("GRPC_ADDR", "")
	cfg.KafkaBrokers = getEnvCSV("KAFKA_BROKERS", "")
	cfg.KafkaTopicEvents = getEnv("KAFKA_EVENTS_TOPIC", "eventnu.events")
	cfg.KafkaPushGroup = getEnv("KAFKA_PUSH_GROUP", "push-fanout")

	cfg.DatabaseURL = getEnv("DATABASE_URL", "")
	cfg.DatabaseAdminURL = getEnv("DATABASE_ADMIN_URL", "")

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("missing required DATABASE_URL")
	}

	cfg.JWTSecret = getEnv("JWT_SECRET", "")
	if cfg.JWTSecret == "" {
		return Config{}, fmt.Errorf("missing required JWT_SECRET")
	}
	cfg.TicketQRSecret = getEnv("TICKET_QR_SECRET", cfg.JWTSecret)

	if cfg.JWTExpiry, err = parseDuration(getEnv("JWT_EXPIRY", "15m")); err != nil {
		return Config{}, fmt.Errorf("JWT_EXPIRY: %w", err)
	}
	if cfg.RefreshTokenExpiry, err = parseDuration(getEnv("REFRESH_TOKEN_EXPIRY", "30d")); err != nil {
		return Config{}, fmt.Errorf("REFRESH_TOKEN_EXPIRY: %w", err)
	}

	if cfg.AuthLoginLimit, err = strconv.Atoi(getEnv("AUTH_LOGIN_RATE_LIMIT", "20")); err != nil {
		return Config{}, fmt.Errorf("AUTH_LOGIN_RATE_LIMIT must be an integer: %w", err)
	}
	if cfg.AuthLoginWindow, err = parseDuration(getEnv("AUTH_LOGIN_RATE_WINDOW", "5m")); err != nil {
		return Config{}, fmt.Errorf("AUTH_LOGIN_RATE_WINDOW: %w", err)
	}
	if cfg.AuthRegisterLimit, err = strconv.Atoi(getEnv("AUTH_REGISTER_RATE_LIMIT", "5")); err != nil {
		return Config{}, fmt.Errorf("AUTH_REGISTER_RATE_LIMIT must be an integer: %w", err)
	}
	if cfg.AuthRegisterWindow, err = parseDuration(getEnv("AUTH_REGISTER_RATE_WINDOW", "1h")); err != nil {
		return Config{}, fmt.Errorf("AUTH_REGISTER_RATE_WINDOW: %w", err)
	}
	if cfg.AuthRefreshLimit, err = strconv.Atoi(getEnv("AUTH_REFRESH_RATE_LIMIT", "30")); err != nil {
		return Config{}, fmt.Errorf("AUTH_REFRESH_RATE_LIMIT must be an integer: %w", err)
	}
	if cfg.AuthRefreshWindow, err = parseDuration(getEnv("AUTH_REFRESH_RATE_WINDOW", "5m")); err != nil {
		return Config{}, fmt.Errorf("AUTH_REFRESH_RATE_WINDOW: %w", err)
	}
	if cfg.AuthVerifyLimit, err = strconv.Atoi(getEnv("AUTH_VERIFY_RATE_LIMIT", "20")); err != nil {
		return Config{}, fmt.Errorf("AUTH_VERIFY_RATE_LIMIT must be an integer: %w", err)
	}
	if cfg.AuthVerifyWindow, err = parseDuration(getEnv("AUTH_VERIFY_RATE_WINDOW", "1h")); err != nil {
		return Config{}, fmt.Errorf("AUTH_VERIFY_RATE_WINDOW: %w", err)
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
	if cfg.DomainEventPollInterval, err = parseDuration(getEnv("DOMAIN_EVENT_POLL_INTERVAL", "1s")); err != nil {
		return Config{}, fmt.Errorf("DOMAIN_EVENT_POLL_INTERVAL: %w", err)
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

	cfg.MediaStorageProvider = getEnv("MEDIA_STORAGE_PROVIDER", "local")
	cfg.MediaS3Endpoint = getEnv("MEDIA_S3_ENDPOINT", "http://localhost:9000")
	cfg.MediaS3Region = getEnv("MEDIA_S3_REGION", "auto")
	cfg.MediaS3Bucket = getEnv("MEDIA_S3_BUCKET", "event-nu")
	cfg.MediaS3AccessKey = getEnv("MEDIA_S3_ACCESS_KEY", "")
	cfg.MediaS3SecretKey = getEnv("MEDIA_S3_SECRET_KEY", "")
	cfg.MediaPublicBase = getEnv("MEDIA_PUBLIC_BASE", cfg.APIPublicBase)
	cfg.MediaLocalDir = getEnv("MEDIA_LOCAL_DIR", "./media-store")
	if cfg.MediaUploadExpiry, err = parseDuration(getEnv("MEDIA_UPLOAD_EXPIRY", "15m")); err != nil {
		return Config{}, fmt.Errorf("MEDIA_UPLOAD_EXPIRY: %w", err)
	}
	if cfg.MediaMaxUploadBytes, err = strconv.ParseInt(getEnv("MEDIA_MAX_UPLOAD_BYTES", "10485760"), 10, 64); err != nil {
		return Config{}, fmt.Errorf("MEDIA_MAX_UPLOAD_BYTES must be an integer: %w", err)
	}
	if cfg.MediaJobPollInterval, err = parseDuration(getEnv("MEDIA_JOB_POLL_INTERVAL", "2s")); err != nil {
		return Config{}, fmt.Errorf("MEDIA_JOB_POLL_INTERVAL: %w", err)
	}
	if cfg.MediaJobBatchSize, err = strconv.Atoi(getEnv("MEDIA_JOB_BATCH_SIZE", "10")); err != nil {
		return Config{}, fmt.Errorf("MEDIA_JOB_BATCH_SIZE must be an integer: %w", err)
	}
	if cfg.MediaJobMaxAttempts, err = strconv.Atoi(getEnv("MEDIA_JOB_MAX_ATTEMPTS", "3")); err != nil {
		return Config{}, fmt.Errorf("MEDIA_JOB_MAX_ATTEMPTS must be an integer: %w", err)
	}
	if cfg.MediaJobRetryBaseDelay, err = parseDuration(getEnv("MEDIA_JOB_RETRY_BASE_DELAY", "30s")); err != nil {
		return Config{}, fmt.Errorf("MEDIA_JOB_RETRY_BASE_DELAY: %w", err)
	}

	if cfg.MediaStorageProvider != "local" && cfg.MediaStorageProvider != "s3" {
		return Config{}, fmt.Errorf("MEDIA_STORAGE_PROVIDER must be \"local\" or \"s3\"")
	}
	if cfg.MediaStorageProvider == "s3" {
		if cfg.MediaS3Endpoint == "" {
			return Config{}, fmt.Errorf("MEDIA_STORAGE_PROVIDER=s3 requires MEDIA_S3_ENDPOINT")
		}
		if cfg.MediaS3Bucket == "" {
			return Config{}, fmt.Errorf("MEDIA_STORAGE_PROVIDER=s3 requires MEDIA_S3_BUCKET")
		}
		if cfg.MediaS3AccessKey == "" || cfg.MediaS3SecretKey == "" {
			return Config{}, fmt.Errorf("MEDIA_STORAGE_PROVIDER=s3 requires MEDIA_S3_ACCESS_KEY and MEDIA_S3_SECRET_KEY")
		}
	}
	if cfg.MediaMaxUploadBytes <= 0 {
		return Config{}, fmt.Errorf("MEDIA_MAX_UPLOAD_BYTES must be > 0")
	}
	if cfg.MediaUploadExpiry <= 0 {
		return Config{}, fmt.Errorf("MEDIA_UPLOAD_EXPIRY must be > 0")
	}
	if cfg.MediaJobBatchSize <= 0 {
		return Config{}, fmt.Errorf("MEDIA_JOB_BATCH_SIZE must be > 0")
	}
	if cfg.MediaJobMaxAttempts <= 0 {
		return Config{}, fmt.Errorf("MEDIA_JOB_MAX_ATTEMPTS must be > 0")
	}
	if cfg.MediaJobRetryBaseDelay <= 0 {
		return Config{}, fmt.Errorf("MEDIA_JOB_RETRY_BASE_DELAY must be > 0")
	}

	if cfg.AnalyticsIngestRateLimit, err = strconv.Atoi(getEnv("ANALYTICS_INGEST_RATE_LIMIT", "600")); err != nil {
		return Config{}, fmt.Errorf("ANALYTICS_INGEST_RATE_LIMIT must be an integer: %w", err)
	}
	if cfg.AnalyticsIngestRateWindow, err = parseDuration(getEnv("ANALYTICS_INGEST_RATE_WINDOW", "1m")); err != nil {
		return Config{}, fmt.Errorf("ANALYTICS_INGEST_RATE_WINDOW: %w", err)
	}
	if cfg.AnalyticsMaxBatch, err = strconv.Atoi(getEnv("ANALYTICS_MAX_BATCH", "100")); err != nil {
		return Config{}, fmt.Errorf("ANALYTICS_MAX_BATCH must be an integer: %w", err)
	}
	if cfg.AnalyticsMaxPropertiesBytes, err = strconv.Atoi(getEnv("ANALYTICS_MAX_PROPERTIES_BYTES", "2048")); err != nil {
		return Config{}, fmt.Errorf("ANALYTICS_MAX_PROPERTIES_BYTES must be an integer: %w", err)
	}
	if cfg.AnalyticsRetentionDays, err = strconv.Atoi(getEnv("ANALYTICS_RETENTION_DAYS", "90")); err != nil {
		return Config{}, fmt.Errorf("ANALYTICS_RETENTION_DAYS must be an integer: %w", err)
	}
	if cfg.AnalyticsRollupPollInterval, err = parseDuration(getEnv("ANALYTICS_ROLLUP_POLL_INTERVAL", "1m")); err != nil {
		return Config{}, fmt.Errorf("ANALYTICS_ROLLUP_POLL_INTERVAL: %w", err)
	}

	if cfg.AnalyticsMaxBatch <= 0 || cfg.AnalyticsMaxBatch > 500 {
		return Config{}, fmt.Errorf("ANALYTICS_MAX_BATCH must be between 1 and 500")
	}
	if cfg.AnalyticsMaxPropertiesBytes <= 0 {
		return Config{}, fmt.Errorf("ANALYTICS_MAX_PROPERTIES_BYTES must be > 0")
	}
	if cfg.AnalyticsRetentionDays <= 0 {
		return Config{}, fmt.Errorf("ANALYTICS_RETENTION_DAYS must be > 0")
	}
	if cfg.AnalyticsRollupPollInterval <= 0 {
		return Config{}, fmt.Errorf("ANALYTICS_ROLLUP_POLL_INTERVAL must be > 0")
	}

	cfg.PaymentProvider = getEnv("PAYMENT_PROVIDER", "noop")
	cfg.ChapaSecretKey = getEnv("CHAPA_SECRET_KEY", "")
	cfg.ChapaAPIBase = getEnv("CHAPA_API_BASE", "https://api.chapa.co/v1")
	cfg.ChapaWebhookSecret = getEnv("CHAPA_WEBHOOK_SECRET", cfg.ChapaSecretKey)
	cfg.ChapaRedirectBase = getEnv("CHAPA_REDIRECT_BASE", cfg.APIPublicBase)

	if cfg.PaymentProvider != "noop" && cfg.PaymentProvider != "chapa" {
		return Config{}, fmt.Errorf("PAYMENT_PROVIDER must be \"noop\" or \"chapa\"")
	}
	if cfg.PaymentProvider == "chapa" {
		if cfg.ChapaSecretKey == "" {
			return Config{}, fmt.Errorf("PAYMENT_PROVIDER=chapa requires CHAPA_SECRET_KEY")
		}
		if cfg.ChapaAPIBase == "" {
			return Config{}, fmt.Errorf("PAYMENT_PROVIDER=chapa requires CHAPA_API_BASE")
		}
		if cfg.ChapaWebhookSecret == "" {
			return Config{}, fmt.Errorf("PAYMENT_PROVIDER=chapa requires CHAPA_WEBHOOK_SECRET")
		}
	}

	if cfg.SyncMaxLimit, err = strconv.Atoi(getEnv("SYNC_MAX_LIMIT", "500")); err != nil {
		return Config{}, fmt.Errorf("SYNC_MAX_LIMIT must be an integer: %w", err)
	}
	if cfg.SyncMaxLimit <= 0 || cfg.SyncMaxLimit > 1000 {
		return Config{}, fmt.Errorf("SYNC_MAX_LIMIT must be between 1 and 1000")
	}

	cfg.PushProvider = getEnv("PUSH_PROVIDER", "noop")
	cfg.FCMServiceAccount = getEnv("FCM_SERVICE_ACCOUNT", "")
	cfg.FCMProjectID = getEnv("FCM_PROJECT_ID", "")
	cfg.FCMAPIBase = getEnv("FCM_API_BASE", "https://fcm.googleapis.com/v1")
	cfg.FCMTokenURL = getEnv("FCM_TOKEN_URL", "https://oauth2.googleapis.com/token")

	if cfg.PushPollInterval, err = parseDuration(getEnv("PUSH_POLL_INTERVAL", "2s")); err != nil {
		return Config{}, fmt.Errorf("PUSH_POLL_INTERVAL: %w", err)
	}
	if cfg.PushBatchSize, err = strconv.Atoi(getEnv("PUSH_BATCH_SIZE", "50")); err != nil {
		return Config{}, fmt.Errorf("PUSH_BATCH_SIZE must be an integer: %w", err)
	}
	if cfg.PushMaxAttempts, err = strconv.Atoi(getEnv("PUSH_MAX_ATTEMPTS", "5")); err != nil {
		return Config{}, fmt.Errorf("PUSH_MAX_ATTEMPTS must be an integer: %w", err)
	}
	if cfg.PushRetryBaseDelay, err = parseDuration(getEnv("PUSH_RETRY_BASE_DELAY", "30s")); err != nil {
		return Config{}, fmt.Errorf("PUSH_RETRY_BASE_DELAY: %w", err)
	}

	if cfg.PushProvider != "noop" && cfg.PushProvider != "fcm" {
		return Config{}, fmt.Errorf("PUSH_PROVIDER must be \"noop\" or \"fcm\"")
	}
	if cfg.PushProvider == "fcm" && cfg.FCMServiceAccount == "" {
		return Config{}, fmt.Errorf("PUSH_PROVIDER=fcm requires FCM_SERVICE_ACCOUNT")
	}

	for _, rl := range []struct {
		limit  int
		window time.Duration
		name   string
	}{
		{cfg.AuthLoginLimit, cfg.AuthLoginWindow, "AUTH_LOGIN"},
		{cfg.AuthRegisterLimit, cfg.AuthRegisterWindow, "AUTH_REGISTER"},
		{cfg.AuthRefreshLimit, cfg.AuthRefreshWindow, "AUTH_REFRESH"},
		{cfg.AuthVerifyLimit, cfg.AuthVerifyWindow, "AUTH_VERIFY"},
		{cfg.AnalyticsIngestRateLimit, cfg.AnalyticsIngestRateWindow, "ANALYTICS_INGEST"},
	} {
		if rl.limit <= 0 {
			return Config{}, fmt.Errorf("%s_RATE_LIMIT must be > 0", rl.name)
		}
		if rl.window <= 0 {
			return Config{}, fmt.Errorf("%s_RATE_WINDOW must be > 0", rl.name)
		}
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getEnvCSV(key, fallback string) []string {
	raw := getEnv(key, fallback)
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
