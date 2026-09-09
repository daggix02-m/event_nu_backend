-- +goose Up
-- Notifications (inbox records; push dispatch is Phase 17), event reminders
-- (records only; dispatch is Phase 17), full-text search on events, and
-- media-id placeholders on events.
--
-- Notes:
--  * notifications are inserted via the SECURITY DEFINER notify_user() helper
--    because creation happens inside a user request for a DIFFERENT user (the
--    recipient); the insert policy would reject an app_user inserting someone
--    else's row. app_user keeps only SELECT + UPDATE (mark-read) grants, per
--    the target schema.
--  * event_reminders grants DELETE (target granted only SELECT/INSERT/UPDATE)
--    so the reminder can be removed by its owner.
--  * events.poster_media_id / teaser_media_id are nullable placeholders with no
--    FK until the media tables land (Phase 13).

CREATE TYPE notification_type AS ENUM ('event_reminder', 'event_update', 'organizer_update', 'comment', 'like', 'follow', 'rsvp', 'ticket', 'moderation', 'system');
CREATE TYPE reminder_status AS ENUM ('scheduled', 'sent', 'cancelled', 'failed');

CREATE TABLE notifications (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  type       notification_type NOT NULL,
  title      text NOT NULL,
  body       text NOT NULL,
  data       jsonb NOT NULL DEFAULT '{}',
  read_at    timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notifications_user_idx ON notifications (user_id, created_at DESC);
CREATE INDEX notifications_unread_idx ON notifications (user_id, created_at DESC) WHERE read_at IS NULL;

CREATE TABLE event_reminders (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id        uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  remind_at       timestamptz NOT NULL,
  status          reminder_status NOT NULL DEFAULT 'scheduled',
  notification_id uuid REFERENCES notifications(id) ON DELETE SET NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  sent_at         timestamptz,
  UNIQUE (user_id, event_id, remind_at)
);
CREATE INDEX reminders_due_idx ON event_reminders (remind_at, status);

-- Media placeholders on events (nullable, FK arrives with the media tables).
ALTER TABLE events ADD COLUMN poster_media_id uuid;
ALTER TABLE events ADD COLUMN teaser_media_id uuid;

-- Full-text search over title + description (column already exists; index now).
CREATE INDEX events_search_idx ON events USING GIN (search_vector);

-- ---- RLS: notifications -----------------------------------------------------
ALTER TABLE notifications ENABLE ROW LEVEL SECURITY;
CREATE POLICY notifications_select_own ON notifications FOR SELECT
  USING (user_id = current_app_user_id() OR is_privileged());
CREATE POLICY notifications_insert_privileged ON notifications FOR INSERT
  WITH CHECK (is_privileged());
CREATE POLICY notifications_update_own ON notifications FOR UPDATE
  USING (user_id = current_app_user_id() OR is_privileged())
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());

-- ---- RLS: event_reminders ---------------------------------------------------
ALTER TABLE event_reminders ENABLE ROW LEVEL SECURITY;
CREATE POLICY event_reminders_own ON event_reminders FOR ALL
  USING (user_id = current_app_user_id() OR is_privileged())
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());

-- Inserts a notification for an arbitrary user without tripping RLS (the
-- inserting request is a different user than the recipient). Caller-supplied
-- type is validated by the enum cast.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION notify_user(p_user_id uuid, p_type text, p_title text, p_body text, p_data jsonb DEFAULT '{}')
RETURNS uuid
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
DECLARE
  v_id uuid;
BEGIN
  INSERT INTO notifications (user_id, type, title, body, data)
  VALUES (p_user_id, p_type::notification_type, p_title, p_body, coalesce(p_data, '{}'::jsonb))
  RETURNING id INTO v_id;
  RETURN v_id;
END;
$$;
-- +goose StatementEnd

GRANT SELECT, UPDATE ON notifications TO app_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON event_reminders TO app_user;
GRANT EXECUTE ON FUNCTION notify_user(uuid, text, text, text, jsonb) TO app_user;

-- +goose Down
DROP FUNCTION IF EXISTS notify_user(uuid, text, text, text, jsonb);
DROP TABLE IF EXISTS event_reminders;
DROP TABLE IF EXISTS notifications;
ALTER TABLE events DROP COLUMN IF EXISTS teaser_media_id;
ALTER TABLE events DROP COLUMN IF EXISTS poster_media_id;
DROP INDEX IF EXISTS events_search_idx;
DROP TYPE IF EXISTS reminder_status;
DROP TYPE IF EXISTS notification_type;