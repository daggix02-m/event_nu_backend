-- +goose Up
-- Push dispatch (Phase 17): the device registry + dispatch bookkeeping on the
-- notifications created by the Phase 12d hooks, plus the worker credential
-- plumbing hooks rely on.
--
--  * notifications gain dispatch columns. The worker (trusted 'service' role)
--    claims a batch, sends to every device of the recipient, and stamps
--    dispatched_at when the loop is done. Transient failures re-schedule via
--    dispatch_attempts / next_dispatch_at (exponential backoff, capped by
--    PUSH_MAX_ATTEMPTS); permanently-unregistered tokens are pruned by the
--    consumer. A notification with zero devices counts as 'dispatched' — nothing
--    to deliver, and re-scanning it forever would waste the poll.
--  * user_devices is app_user-managed (register/deregister run under the API's
--    normal identity + RLS) so devices can't be read or removed by anyone else.
--    The unique token constraint makes registration idempotent per device and
--    reassigns the row when a different user signs in on the same device.
--
-- Existing inbox rows from before this migration all have next_dispatch_at =
-- now() (column default), so they are re-dispatched — harmless, the app upserts
-- and marks them read client-side.

ALTER TABLE notifications
  ADD COLUMN dispatch_attempts int NOT NULL DEFAULT 0,
  ADD COLUMN dispatched_at     timestamptz,
  ADD COLUMN next_dispatch_at  timestamptz NOT NULL DEFAULT now(),
  ADD COLUMN dispatch_lease_at timestamptz;

CREATE INDEX notifications_dispatch_idx
  ON notifications (next_dispatch_at, created_at)
  WHERE dispatched_at IS NULL;

-- Lease column so two worker shards never claim the same due reminder.
ALTER TABLE event_reminders ADD COLUMN dispatch_lease_at timestamptz;

CREATE TABLE user_devices (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token        text NOT NULL,
  platform     text NOT NULL DEFAULT 'android',
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (token)
);
CREATE INDEX user_devices_user_idx ON user_devices (user_id);

-- ---- RLS: user_devices ------------------------------------------------------
ALTER TABLE user_devices ENABLE ROW LEVEL SECURITY;
CREATE POLICY user_devices_own ON user_devices FOR ALL
  USING (user_id = current_app_user_id() OR is_privileged())
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());

GRANT SELECT, DELETE ON user_devices TO app_user;

-- Registers the caller's device token. Runs SECURITY DEFINER for the same
-- reason notify_user does: the on-conflict path may UPDATE a row that a
-- different user registered on the same phone (a reinstall or re-login), which
-- app_user RLS forbids. The helper returns the row via RETURN QUERY, so the
-- caller gets the freshly registered device in one statement — a SELECT cannot
-- read a row its own subquery just inserted (snapshot precedes the write).
-- +goose StatementBegin
-- Output columns are out_-prefixed so they can't shadow the table's own column
-- names in the INSERT: an OUT parameter named `token` would make the
-- ON CONFLICT (token) reference ambiguous.
CREATE OR REPLACE FUNCTION upsert_user_device(p_user_id uuid, p_token text, p_platform text)
RETURNS TABLE (out_id uuid, out_user_id uuid, out_token text, out_platform text, out_last_seen_at timestamptz, out_created_at timestamptz)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
BEGIN
  RETURN QUERY
  INSERT INTO user_devices (user_id, token, platform)
  VALUES (p_user_id, p_token, p_platform)
  ON CONFLICT (token) DO UPDATE
    SET user_id = EXCLUDED.user_id,
        platform = EXCLUDED.platform,
        last_seen_at = now()
  RETURNING user_devices.id, user_devices.user_id, user_devices.token,
            user_devices.platform, user_devices.last_seen_at, user_devices.created_at;
END;
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION upsert_user_device(uuid, text, text) TO app_user;

-- +goose Down
DROP FUNCTION IF EXISTS upsert_user_device(uuid, text, text);
DROP TABLE IF EXISTS user_devices;
ALTER TABLE event_reminders DROP COLUMN IF EXISTS dispatch_lease_at;
DROP INDEX IF EXISTS notifications_dispatch_idx;
ALTER TABLE notifications DROP COLUMN IF EXISTS dispatch_lease_at;
ALTER TABLE notifications DROP COLUMN IF EXISTS next_dispatch_at;
ALTER TABLE notifications DROP COLUMN IF EXISTS dispatched_at;
ALTER TABLE notifications DROP COLUMN IF EXISTS dispatch_attempts;