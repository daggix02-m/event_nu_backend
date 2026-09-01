-- +goose Up
CREATE TYPE email_status AS ENUM ('pending', 'sent', 'failed', 'skipped');

-- One-time codes for email verification, login OTP, and password reset.
-- Plaintext is never stored; only the SHA-256 hash is kept for validation.
CREATE TABLE email_codes (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  purpose magic_link_purpose NOT NULL,
  code_hash text NOT NULL,
  attempts int NOT NULL DEFAULT 0,
  max_attempts int NOT NULL DEFAULT 5,
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX email_codes_hash_idx ON email_codes (code_hash);
CREATE INDEX email_codes_user_idx ON email_codes (user_id);

-- Transactional outbox: handlers INSERT here and return; the worker sends.
CREATE TABLE email_outbox (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  recipient_email text NOT NULL,
  recipient_name text,
  template_id int NOT NULL,
  params jsonb NOT NULL DEFAULT '{}',
  status email_status NOT NULL DEFAULT 'pending',
  attempts int NOT NULL DEFAULT 0,
  max_attempts int NOT NULL DEFAULT 3,
  next_retry_at timestamptz NOT NULL DEFAULT now(),
  brevo_message_id text,
  last_error text,
  idempotency_key text UNIQUE,
  sent_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX email_outbox_pending_idx ON email_outbox (status, next_retry_at);

-- Append-only send record for observability/audit.
CREATE TABLE email_logs (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  outbox_id uuid REFERENCES email_outbox(id) ON DELETE SET NULL,
  recipient_email text NOT NULL,
  template_id int NOT NULL,
  status email_status NOT NULL,
  brevo_message_id text,
  error text,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX email_logs_outbox_idx ON email_logs (outbox_id);

-- +goose Down
DROP TABLE IF EXISTS email_logs;
DROP TABLE IF EXISTS email_outbox;
DROP TABLE IF EXISTS email_codes;
DROP TYPE IF EXISTS email_status;