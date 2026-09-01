-- +goose Up
CREATE TABLE auth_sessions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  refresh_hash text NOT NULL UNIQUE,
  user_agent text,
  ip_hash text,
  device_name text,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE magic_link_tokens (
  token_hash text PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  purpose magic_link_purpose NOT NULL,
  expires_at timestamptz NOT NULL,
  used_at timestamptz
);

CREATE INDEX auth_sessions_user_id_idx ON auth_sessions (user_id);
CREATE INDEX magic_link_tokens_user_id_idx ON magic_link_tokens (user_id);

-- +goose Down
DROP TABLE IF EXISTS magic_link_tokens;
DROP TABLE IF EXISTS auth_sessions;