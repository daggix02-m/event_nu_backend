-- +goose Up
-- Client-verifiable idempotency for mobile retryable writes (spec §8.29/§22).
-- Each protected POST records the client-supplied Idempotency-Key; a retry with
-- the same key + identical request replays the stored response instead of
-- creating a duplicate row; the same key + different request returns a 409.
CREATE TYPE idempotency_status AS ENUM ('in_progress', 'completed');

CREATE TABLE idempotency_keys (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key text NOT NULL,
  request_hash text NOT NULL,
  operation text NOT NULL,
  status idempotency_status NOT NULL DEFAULT 'in_progress',
  response_code integer,
  response_body jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  UNIQUE (user_id, key)
);

-- A user looks up their own keys by user id; the expiry index lets the repo
-- opportunistically reap expired rows on each create.
CREATE INDEX idempotency_keys_user_idx ON idempotency_keys (user_id);
CREATE INDEX idempotency_keys_expires_idx ON idempotency_keys (expires_at);

-- ---- RLS: idempotency_keys ------------------------------------------------
-- Own rows only via the transaction-local user id; privileged service/admin
-- contexts bypass RLS for cleanup/inspection.
ALTER TABLE idempotency_keys ENABLE ROW LEVEL SECURITY;
CREATE POLICY idempotency_keys_own ON idempotency_keys FOR ALL
  USING (user_id = current_app_user_id() OR is_privileged())
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());

GRANT SELECT, INSERT, UPDATE, DELETE ON idempotency_keys TO app_user;

-- +goose Down
DROP TABLE IF EXISTS idempotency_keys;
DROP TYPE IF EXISTS idempotency_status;
