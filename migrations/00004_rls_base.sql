-- +goose Up
-- Base RLS framework: context helper functions, grants, and policies for the
-- tables created in migrations 00001-00003. Passwords for roles are provisioned
-- per environment (Neon role management) and never stored in migrations.

-- Roles (idempotent). app_user is the API/worker role; worker_role is reserved
-- for trusted background workers; admin_dashboard for the Next.js/Prisma admin.
-- +goose StatementBegin
DO $do$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'worker_role') THEN
    CREATE ROLE worker_role NOLOGIN;
  END IF;
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'admin_dashboard') THEN
    CREATE ROLE admin_dashboard NOLOGIN;
  END IF;
END
$do$;
-- +goose StatementEnd

-- Context helper functions (transaction-local via set_config(... , true)).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION current_app_user_id() RETURNS uuid
LANGUAGE sql STABLE AS $$
  SELECT NULLIF(current_setting('app.user_id', true), '')::uuid;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION current_app_role() RETURNS text
LANGUAGE sql STABLE AS $$
  SELECT NULLIF(current_setting('app.role', true), '');
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION is_admin() RETURNS boolean
LANGUAGE sql STABLE AS $$
  SELECT current_app_role() = 'admin';
$$;
-- +goose StatementEnd

-- True for admin OR the Go worker's trusted internal (service) context.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION is_privileged() RETURNS boolean
LANGUAGE sql STABLE AS $$
  SELECT current_app_role() IN ('admin', 'service');
$$;
-- +goose StatementEnd

-- ---- users -------------------------------------------------------------
ALTER TABLE users ENABLE ROW LEVEL SECURITY;

CREATE POLICY users_select_own ON users FOR SELECT
  USING (id = current_app_user_id() OR is_privileged());
CREATE POLICY users_insert ON users FOR INSERT
  WITH CHECK (true);
CREATE POLICY users_update_own ON users FOR UPDATE
  USING (id = current_app_user_id() OR is_privileged())
  WITH CHECK (id = current_app_user_id() OR is_privileged());

-- ---- auth_sessions -----------------------------------------------------
ALTER TABLE auth_sessions ENABLE ROW LEVEL SECURITY;

CREATE POLICY auth_sessions_own ON auth_sessions FOR ALL
  USING (user_id = current_app_user_id() OR is_privileged())
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());

-- ---- magic_link_tokens -------------------------------------------------
ALTER TABLE magic_link_tokens ENABLE ROW LEVEL SECURITY;

CREATE POLICY magic_link_tokens_own ON magic_link_tokens FOR ALL
  USING (user_id = current_app_user_id() OR is_privileged())
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());

-- ---- email_codes -------------------------------------------------------
ALTER TABLE email_codes ENABLE ROW LEVEL SECURITY;

-- The owner redeems a code (SELECT to validate, UPDATE attempts/consumed_at).
CREATE POLICY email_codes_own_select ON email_codes FOR SELECT
  USING (user_id = current_app_user_id() OR is_privileged());
CREATE POLICY email_codes_own_update ON email_codes FOR UPDATE
  USING (user_id = current_app_user_id() OR is_privileged())
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());

-- ---- email_outbox ------------------------------------------------------
ALTER TABLE email_outbox ENABLE ROW LEVEL SECURITY;

-- The app inserts into the outbox; the worker (service context) reads/updates.
CREATE POLICY email_outbox_insert ON email_outbox FOR INSERT
  WITH CHECK (true);
CREATE POLICY email_outbox_worker_select ON email_outbox FOR SELECT
  USING (is_privileged());
CREATE POLICY email_outbox_worker_update ON email_outbox FOR UPDATE
  USING (is_privileged())
  WITH CHECK (is_privileged());

-- ---- email_logs --------------------------------------------------------
ALTER TABLE email_logs ENABLE ROW LEVEL SECURITY;

CREATE POLICY email_logs_privileged_select ON email_logs FOR SELECT
  USING (is_privileged());
CREATE POLICY email_logs_privileged_insert ON email_logs FOR INSERT
  WITH CHECK (is_privileged());

-- +goose Down
-- Dropping policies / disabling RLS. Tables are kept; the framework is
-- re-applied via a later migration if needed.
ALTER TABLE email_logs DISABLE ROW LEVEL SECURITY;
ALTER TABLE email_outbox DISABLE ROW LEVEL SECURITY;
ALTER TABLE email_codes DISABLE ROW LEVEL SECURITY;
ALTER TABLE magic_link_tokens DISABLE ROW LEVEL SECURITY;
ALTER TABLE auth_sessions DISABLE ROW LEVEL SECURITY;
ALTER TABLE users DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS email_logs_privileged_select ON email_logs;
DROP POLICY IF EXISTS email_logs_privileged_insert ON email_logs;
DROP POLICY IF EXISTS email_outbox_worker_select ON email_outbox;
DROP POLICY IF EXISTS email_outbox_worker_update ON email_outbox;
DROP POLICY IF EXISTS email_outbox_insert ON email_outbox;
DROP POLICY IF EXISTS email_codes_own_select ON email_codes;
DROP POLICY IF EXISTS email_codes_own_update ON email_codes;
DROP POLICY IF EXISTS magic_link_tokens_own ON magic_link_tokens;
DROP POLICY IF EXISTS auth_sessions_own ON auth_sessions;
DROP POLICY IF EXISTS users_update_own ON users;
DROP POLICY IF EXISTS users_insert ON users;
DROP POLICY IF EXISTS users_select_own ON users;

DROP FUNCTION IF EXISTS is_privileged();
DROP FUNCTION IF EXISTS is_admin();
DROP FUNCTION IF EXISTS current_app_role();
DROP FUNCTION IF EXISTS current_app_user_id();