-- +goose Up
-- Interactive onboarding, per-user category preferences and first-party
-- analytics (Phase 33).
--
--  * users.onboarding_completed_at stamps when a user finished (or explicitly
--    skipped) the onboarding flow so the client can stop redirecting them.
--  * user_category_preferences stores the user's interest category ids as an
--    ordered array. Full-replace PUT semantics make the single row atomic and
--    idempotent by construction (no change-tracking, no history).
--  * analytics_events is the raw event log. Clients POST batches; the API
--    preserves attribution by trusting ONLY the authenticated user id (the
--    per-session user_id must equal the caller or be NULL for anonymous), so a
--    leaked or misconfigured client can never fabricate another user's data.
--  * analytics_daily_feature_usage is the UTC-day rollup produced by the
--    analytics-rollup worker; the admin dashboard reads ONLY this table (raw
--    events remain privileged-worker data, never directly readable by admins).

-- ---- users.onboarding_completed_at ----------------------------------------
ALTER TABLE users ADD COLUMN onboarding_completed_at timestamptz;

-- ---- user_category_preferences --------------------------------------------
CREATE TABLE user_category_preferences (
  user_id     uuid NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  category_ids uuid[] NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT user_category_preferences_nonempty_chk CHECK (cardinality(category_ids) > 0)
);

-- ---- RLS: user_category_preferences ---------------------------------------
ALTER TABLE user_category_preferences ENABLE ROW LEVEL SECURITY;

-- Own preferences only (or privileged). The admin dashboard is expected to
-- read user category preferences via the service role if it ever needs them.
CREATE POLICY user_category_preferences_select ON user_category_preferences FOR SELECT
  USING (user_id = current_app_user_id() OR is_privileged());
CREATE POLICY user_category_preferences_insert ON user_category_preferences FOR INSERT
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());
CREATE POLICY user_category_preferences_update ON user_category_preferences FOR UPDATE
  USING (user_id = current_app_user_id() OR is_privileged())
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());
CREATE POLICY user_category_preferences_delete ON user_category_preferences FOR DELETE
  USING (user_id = current_app_user_id() OR is_privileged());

GRANT SELECT, INSERT, UPDATE, DELETE ON user_category_preferences TO app_user;

-- ---- analytics_events ------------------------------------------------------
CREATE TABLE analytics_events (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id  uuid NOT NULL,
  event_name  text NOT NULL,
  user_id     uuid REFERENCES users(id) ON DELETE SET NULL,
  properties  jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT analytics_events_name_len_chk CHECK (char_length(event_name) BETWEEN 1 AND 64)
);

CREATE INDEX analytics_events_created_idx ON analytics_events (created_at);
CREATE INDEX analytics_events_user_created_idx ON analytics_events (user_id, created_at);
CREATE INDEX analytics_events_name_created_idx ON analytics_events (event_name, created_at);

-- ---- RLS: analytics_events ------------------------------------------------
ALTER TABLE analytics_events ENABLE ROW LEVEL SECURITY;

-- The caller can only ever insert events attributed to themselves (or carry
-- NULL user_id for anonymous sessions). Combined with the handler keeping the
-- connection under RLS, a client cannot spoof attribution to another user.
CREATE POLICY analytics_events_insert ON analytics_events FOR INSERT
  WITH CHECK (user_id IS NULL OR user_id = current_app_user_id());
-- Raw analytics are privileged-only (service role handles retention; the admin
-- role may NOT read them — dashboards read the rollup table instead).
CREATE POLICY analytics_events_privileged_select ON analytics_events FOR SELECT
  USING (is_privileged());
CREATE POLICY analytics_events_privileged_delete ON analytics_events FOR DELETE
  USING (is_privileged());

GRANT SELECT, INSERT, DELETE ON analytics_events TO app_user;

-- ---- analytics_daily_feature_usage ----------------------------------------
CREATE TABLE analytics_daily_feature_usage (
  day          date NOT NULL,
  event_name   text NOT NULL,
  event_count  bigint NOT NULL DEFAULT 0,
  unique_users bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (day, event_name),
  CONSTRAINT analytics_daily_name_len_chk CHECK (char_length(event_name) BETWEEN 1 AND 64)
);

-- ---- RLS: analytics_daily_feature_usage -----------------------------------
ALTER TABLE analytics_daily_feature_usage ENABLE ROW LEVEL SECURITY;

-- Admins read the rollup; the worker (service) writes it. Non-privileged roles
-- (app users) have no read path at all.
CREATE POLICY analytics_daily_privileged_select ON analytics_daily_feature_usage FOR SELECT
  USING (is_privileged());
CREATE POLICY analytics_daily_privileged_insert ON analytics_daily_feature_usage FOR INSERT
  WITH CHECK (is_privileged());
CREATE POLICY analytics_daily_privileged_update ON analytics_daily_feature_usage FOR UPDATE
  USING (is_privileged())
  WITH CHECK (is_privileged());

GRANT SELECT, INSERT, UPDATE ON analytics_daily_feature_usage TO app_user;

-- +goose Down
DROP TABLE IF EXISTS analytics_daily_feature_usage;
DROP TABLE IF EXISTS analytics_events;
DROP TABLE IF EXISTS user_category_preferences;
ALTER TABLE users DROP COLUMN IF EXISTS onboarding_completed_at;