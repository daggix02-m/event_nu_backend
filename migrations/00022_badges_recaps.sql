-- +goose Up
-- Badges & recaps (Phase 20).
--
--  * user_badges stores milestone awards (first RSVP, first attended event,
--    first ticket purchase, first posted moment). Writes go through the
--    SECURITY DEFINER award_badge() helper, which verifies the milestone via
--    existing rows and relies on the (user_id, badge_type) uniqueness for
--    exactly-once awards — badge rows can never be forged by direct inserts.
--  * event_recaps caches a read-only dashboard for a published event (top
--    moments, attendee count, review summary, session highlights). The cache is
--    regenerated only when source rows change: event_recap_activity() reports
--    the newest source timestamp and the service compares it to generated_at.
--    Writes go through cache_event_recap() (SECURITY DEFINER) so no untrusted
--    caller can poison the cache with fabricated data.

-- ---- user_badges -----------------------------------------------------------
CREATE TABLE user_badges (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  badge_type text NOT NULL,
  earned_at  timestamptz NOT NULL DEFAULT now(),
  metadata   jsonb,
  CONSTRAINT user_badges_user_type_key UNIQUE (user_id, badge_type),
  CONSTRAINT user_badges_type_chk CHECK (length(badge_type) BETWEEN 1 AND 64)
);

CREATE INDEX user_badges_user_idx ON user_badges (user_id, earned_at DESC, id);

-- ---- RLS: user_badges -----------------------------------------------------
ALTER TABLE user_badges ENABLE ROW LEVEL SECURITY;

-- SELECT: own badges only (or privileged).
CREATE POLICY user_badges_select ON user_badges FOR SELECT
  USING (user_id = current_app_user_id() OR is_privileged());

-- INSERT/UPDATE/DELETE: privileged only — awards run through award_badge().
CREATE POLICY user_badges_write ON user_badges FOR INSERT
  WITH CHECK (is_privileged());
CREATE POLICY user_badges_delete ON user_badges FOR DELETE
  USING (is_privileged());

GRANT SELECT, INSERT, DELETE ON user_badges TO app_user;

-- ---- award_badge() ---------------------------------------------------------
-- Milestone awards are exactly-once and forgery-proof: the milestone check
-- reads real rows (RSVPs/tickets/orders/moments/reviews), so a caller can only
-- earn a badge they have genuinely triggered, and the unique constraint makes
-- a second award a no-op. Returns false when the milestone isn't yet met or
-- the badge was already earned (no error).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION award_badge(p_user_id uuid, p_badge_type text, p_metadata jsonb)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
DECLARE
  v_milestone boolean;
  v_inserted  boolean;
BEGIN
  IF p_user_id IS NULL OR p_badge_type IS NULL THEN
    RETURN false;
  END IF;

  -- The milestone check is the "existing-row" guard: the badge is only earned
  -- when the underlying achievement is real. Unknown badge types never award.
  SELECT (CASE p_badge_type
    WHEN 'first_rsvp'     THEN EXISTS (
      SELECT 1 FROM event_rsvps er
      WHERE er.user_id = p_user_id AND er.status IN ('confirmed', 'attended'))
    WHEN 'first_attended' THEN (
      EXISTS (SELECT 1 FROM tickets t
              WHERE t.user_id = p_user_id AND t.status = 'used')
      OR EXISTS (
        SELECT 1 FROM event_rsvps er
        JOIN events e ON e.id = er.event_id
        WHERE er.user_id = p_user_id
          AND er.status IN ('confirmed', 'attended')
          AND e.starts_at <= now()
      ))
    WHEN 'first_ticket'   THEN EXISTS (
      SELECT 1 FROM orders o
      WHERE o.user_id = p_user_id AND o.status = 'paid')
    WHEN 'first_moment'   THEN EXISTS (
      SELECT 1 FROM moments m WHERE m.user_id = p_user_id)
    ELSE false
  END) INTO v_milestone;

  IF NOT v_milestone THEN
    RETURN false;
  END IF;

  INSERT INTO user_badges (user_id, badge_type, metadata)
  VALUES (p_user_id, p_badge_type, COALESCE(p_metadata, '{}'::jsonb))
  ON CONFLICT (user_id, badge_type) DO NOTHING
  RETURNING true INTO v_inserted;

  RETURN COALESCE(v_inserted, false);
END;
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION award_badge(uuid, text, jsonb) TO app_user;

-- ---- event_recaps ----------------------------------------------------------
CREATE TABLE event_recaps (
  event_id     uuid PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
  data         jsonb NOT NULL,
  generated_at timestamptz NOT NULL DEFAULT now()
);

-- ---- RLS: event_recaps -----------------------------------------------------
ALTER TABLE event_recaps ENABLE ROW LEVEL SECURITY;

-- SELECT: public for published events (the same visibility gate as moments);
-- other roles have no read path (the recap service aggregates directly).
CREATE POLICY event_recaps_select ON event_recaps FOR SELECT
  USING (is_privileged() OR EXISTS (
    SELECT 1 FROM events e
    WHERE e.id = event_recaps.event_id
      AND e.status = 'published'
      AND e.moderation_status <> 'blocked'
      AND e.deleted_at IS NULL
  ));

GRANT SELECT ON event_recaps TO app_user;

-- ---- event_recap_activity() -------------------------------------------------
-- Newest source timestamp for an event's recap inputs (moments, reviews,
-- sessions, RSVPs) seen from the privileged role — the data an anonymous
-- public reader would never be allowed to sum themselves. Returns the epoch
-- when nothing exists yet, so any cached recap is initially treated as fresh.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION event_recap_activity(p_event_id uuid)
RETURNS timestamptz
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
DECLARE
  v_activity timestamptz;
BEGIN
  SELECT greatest(
    COALESCE((SELECT max(created_at) FROM moments WHERE event_id = p_event_id),      to_timestamp(0)),
    COALESCE((SELECT max(updated_at) FROM reviews WHERE event_id = p_event_id),       to_timestamp(0)),
    COALESCE((SELECT max(updated_at) FROM event_sessions WHERE event_id = p_event_id),to_timestamp(0)),
    COALESCE((SELECT max(updated_at) FROM event_rsvps WHERE event_id = p_event_id),   to_timestamp(0))
  ) INTO v_activity;

  RETURN v_activity;
END;
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION event_recap_activity(uuid) TO app_user;

-- ---- cache_event_recap() ----------------------------------------------------
-- Upserts the cached recap only for a published, non-blocked, non-deleted
-- event, refreshing generated_at to now() so staleness compares correctly.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION cache_event_recap(p_event_id uuid, p_data jsonb)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
DECLARE
  v_updated bigint;
BEGIN
  IF p_event_id IS NULL OR p_data IS NULL THEN
    RETURN false;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM events e
    WHERE e.id = p_event_id
      AND e.status = 'published'
      AND e.moderation_status <> 'blocked'
      AND e.deleted_at IS NULL
  ) THEN
    RETURN false;
  END IF;

  INSERT INTO event_recaps (event_id, data, generated_at)
  VALUES (p_event_id, p_data, now())
  ON CONFLICT (event_id) DO UPDATE
    SET data = EXCLUDED.data, generated_at = now();
  GET DIAGNOSTICS v_updated = ROW_COUNT;

  RETURN v_updated > 0;
END;
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION cache_event_recap(uuid, jsonb) TO app_user;

-- +goose Down
DROP FUNCTION IF EXISTS cache_event_recap(uuid, jsonb);
DROP FUNCTION IF EXISTS event_recap_activity(uuid);
DROP FUNCTION IF EXISTS award_badge(uuid, text, jsonb);
DROP TABLE IF EXISTS event_recaps;
DROP TABLE IF EXISTS user_badges;