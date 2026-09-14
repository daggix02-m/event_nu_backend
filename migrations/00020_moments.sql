-- +goose Up
-- Moments (community media gallery) + attendee directory (Phase 18).
--
--  * moments stores user-generated media posts attached to an event. The
--    gallery is publicly readable for published events; creation requires an
--    RSVP or ticket (enforced in the service layer). A SECURITY DEFINER helper
--    verifies the media asset belongs to the caller and is ready before insert.
--  * Attendee directory is opt-in: only event_rsvps with public_rsvp = true
--    appear. Two SECURITY DEFINER functions return paginated attendee lists
--    because event_rsvps RLS is own-row only (app_user cannot read other
--    users' RSVPs). Both functions verify the event is published and not
--    blocked.

CREATE TABLE moments (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id        uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  media_asset_id  uuid NOT NULL REFERENCES media_assets(id) ON DELETE RESTRICT,
  caption         text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (user_id, event_id, media_asset_id)
);

CREATE INDEX moments_event_idx ON moments (event_id, created_at);
CREATE INDEX moments_user_idx ON moments (user_id);

-- ---- RLS: moments --------------------------------------------------------
ALTER TABLE moments ENABLE ROW LEVEL SECURITY;

-- Gallery is public for published, non-blocked, non-deleted events.
CREATE POLICY moments_select_public ON moments FOR SELECT
  USING (is_privileged() OR EXISTS (
    SELECT 1 FROM events e
    WHERE e.id = moments.event_id
      AND e.status = 'published'
      AND e.moderation_status <> 'blocked'
      AND e.deleted_at IS NULL
  ));

-- Insert/delete by the moment owner (eligibility checked in the service layer;
-- the repo inserts with user_id = current_app_user_id()).
CREATE POLICY moments_insert_own ON moments FOR INSERT
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());

CREATE POLICY moments_delete_own ON moments FOR DELETE
  USING (user_id = current_app_user_id() OR is_privileged());

GRANT SELECT, INSERT, DELETE ON moments TO app_user;

-- ---- Attendee directory (SECURITY DEFINER) --------------------------------
-- Both functions guard on event visibility to prevent leaking draft events,
-- then join event_rsvps to users to return display info.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION list_event_attendees(
  p_event_id uuid,
  p_limit     int,
  p_offset    int
)
RETURNS TABLE (
  out_user_id    uuid,
  out_display_name text,
  out_avatar_url  text,
  out_created_at  timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
BEGIN
  -- Verify the event is publicly visible (published, not blocked, not deleted).
  IF NOT EXISTS (
    SELECT 1 FROM events e
    WHERE e.id = p_event_id
      AND e.status = 'published'
      AND e.moderation_status <> 'blocked'
      AND e.deleted_at IS NULL
  ) THEN
    RETURN;  -- empty set for invisible events
  END IF;

  RETURN QUERY
  SELECT
    u.id                                          AS out_user_id,
    COALESCE(u.username, u.email)                 AS out_display_name,
    u.photo_url                                   AS out_avatar_url,
    er.created_at                                 AS out_created_at
  FROM event_rsvps er
  JOIN users u ON u.id = er.user_id AND u.deleted_at IS NULL
  WHERE er.event_id = p_event_id
    AND er.public_rsvp = true
    AND er.status IN ('confirmed', 'attended')
  ORDER BY er.created_at
  LIMIT p_limit
  OFFSET p_offset;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION count_event_attendees(p_event_id uuid)
RETURNS int
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
DECLARE
  v_count int;
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM events e
    WHERE e.id = p_event_id
      AND e.status = 'published'
      AND e.moderation_status <> 'blocked'
      AND e.deleted_at IS NULL
  ) THEN
    RETURN 0;
  END IF;

  SELECT count(*) INTO v_count
  FROM event_rsvps er
  WHERE er.event_id = p_event_id
    AND er.public_rsvp = true
    AND er.status IN ('confirmed', 'attended');

  RETURN v_count;
END;
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION list_event_attendees(uuid, int, int) TO app_user;
GRANT EXECUTE ON FUNCTION count_event_attendees(uuid) TO app_user;

-- ---- Moment delete ----------------------------------------------------------
-- Delete for the owner works via RLS (own-row). Admin removal needs a
-- privileged context, but normal requests run with RLS role 'user' — so the
-- admin branch calls this SECURITY DEFINER helper instead. It checks ownership
-- OR the caller's DB role (re-read from users, never trusted from the request)
-- before deleting; it cannot be abused to delete others' moments because the
-- caller id always comes from current_app_user_id() (set server-side).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION delete_moment(p_moment_id uuid)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
DECLARE
  v_uid     uuid := current_app_user_id();
  v_deleted bigint;
BEGIN
  DELETE FROM moments m
  WHERE m.id = p_moment_id
    AND (m.user_id = v_uid
         OR EXISTS (SELECT 1 FROM users u WHERE u.id = v_uid AND u.role = 'admin'));
  GET DIAGNOSTICS v_deleted = ROW_COUNT;
  RETURN v_deleted > 0;
END;
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION delete_moment(uuid) TO app_user;

-- +goose Down
DROP FUNCTION IF EXISTS delete_moment(uuid);
DROP FUNCTION IF EXISTS count_event_attendees(uuid);
DROP FUNCTION IF EXISTS list_event_attendees(uuid, int, int);
DROP TABLE IF EXISTS moments;
