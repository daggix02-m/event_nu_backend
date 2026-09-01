-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TYPE user_role AS ENUM ('user', 'admin');
CREATE TYPE user_status AS ENUM ('active', 'suspended', 'deactivated');
CREATE TYPE magic_link_purpose AS ENUM ('login', 'verify', 'reset_password');

CREATE TABLE users (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email text NOT NULL UNIQUE,
  password_hash text,
  username text UNIQUE,
  bio text,
  photo_url text,
  role user_role NOT NULL DEFAULT 'user',
  is_verified boolean NOT NULL DEFAULT false,
  privacy jsonb NOT NULL DEFAULT '{}',
  status user_status NOT NULL DEFAULT 'active',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz
);

-- +goose Down
DROP TABLE IF EXISTS users;
DROP TYPE IF EXISTS magic_link_purpose;
DROP TYPE IF EXISTS user_status;
DROP TYPE IF EXISTS user_role;
DROP EXTENSION IF EXISTS pgcrypto;