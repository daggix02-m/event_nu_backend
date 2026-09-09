-- +goose Up
-- RSVP, reviews, reports (Phase 12c). RSVP capacity is enforced atomically by a
-- SECURITY DEFINER function that takes a row lock on the event, counts active
-- RSVPs under that lock, and inserts — no oversell even under concurrency.
-- Reviews gate on attendance (RSVP + event ended); reports record a moderation
-- flag transition to 'under_review' only (never auto-hide). Capacity uses the
-- existing events.max_attendees column.
--
-- RLS notes / deviations from the target schema:
--  * event_rsvps grants DELETE (target granted only SELECT/INSERT/UPDATE):
--    an RSVP toggle is DELETE + re-INSERT so re-RSVP after cancellation works
--    and capacity is released. Capacity counting bypasses own-row RLS via the
--    SECURITY DEFINER helper.
--  * users and venues gain a moderation_status column (target omitted both);
--    they are NOT hidden by 'under_review' — venues keep status='active'.
--  * report_entity_type includes 'organizer' for parity with the target enum,
--    though only event/user/venue/comment targets are exposed in Phase 12c.

CREATE TYPE rsvp_status AS ENUM ('confirmed', 'waitlisted', 'cancelled', 'attended', 'no_show');
CREATE TYPE review_status AS ENUM ('published', 'hidden', 'blocked', 'deleted');
CREATE TYPE report_entity_type AS ENUM ('event', 'user', 'venue', 'comment', 'organizer');
CREATE TYPE report_status AS ENUM ('open', 'under_review', 'resolved', 'rejected');

ALTER TABLE users ADD COLUMN moderation_status moderation_status NOT NULL DEFAULT 'clean';
ALTER TABLE venues ADD COLUMN moderation_status moderation_status NOT NULL DEFAULT 'clean';

-- ---- event_rsvps ------------------------------------------------------------
CREATE TABLE event_rsvps (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id     uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status       rsvp_status NOT NULL DEFAULT 'confirmed',
  quantity     integer NOT NULL DEFAULT 1,
  public_rsvp  boolean NOT NULL DEFAULT true,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (user_id, event_id),
  CONSTRAINT event_rsvps_quantity_chk CHECK (quantity > 0)
);
CREATE INDEX event_rsvps_event_idx ON event_rsvps (event_id, status);
CREATE INDEX event_rsvps_user_idx ON event_rsvps (user_id, status);

-- Atomic capacity reservation: locks the event row, counts active RSVPs under
-- the lock, then inserts. Returns a status string for the Go layer. Runs as the
-- table owner so the count sees every RSVP regardless of the caller's RLS.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION attempt_event_rsvp(p_event_id uuid, p_user_id uuid, p_public_rsvp boolean)
RETURNS text
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
DECLARE
  v_capacity integer;
  v_rsvps    integer;
  v_status   event_status;
  v_uid      uuid := current_app_user_id();
BEGIN
  IF v_uid IS NULL OR v_uid <> p_user_id THEN
    RETURN 'forbidden';
  END IF;
  SELECT max_attendees, status INTO v_capacity, v_status
  FROM events WHERE id = p_event_id FOR UPDATE;
  IF NOT FOUND THEN
    RETURN 'not_found';
  END IF;
  IF v_status <> 'published' THEN
    RETURN 'event_unavailable';
  END IF;
  SELECT count(*) INTO v_rsvps
  FROM event_rsvps WHERE event_id = p_event_id AND status = 'confirmed';
  IF v_capacity IS NOT NULL AND v_rsvps >= v_capacity THEN
    RETURN 'capacity_full';
  END IF;
  INSERT INTO event_rsvps (event_id, user_id, status, quantity, public_rsvp)
  VALUES (p_event_id, p_user_id, 'confirmed', 1, coalesce(p_public_rsvp, true));
  RETURN 'ok';
EXCEPTION WHEN unique_violation THEN
  RETURN 'duplicate';
END;
$$;
-- +goose StatementEnd

ALTER TABLE event_rsvps ENABLE ROW LEVEL SECURITY;
CREATE POLICY event_rsvps_select_own ON event_rsvps FOR SELECT
  USING (user_id = current_app_user_id() OR is_privileged());
CREATE POLICY event_rsvps_insert_own ON event_rsvps FOR INSERT
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());
CREATE POLICY event_rsvps_update_own ON event_rsvps FOR UPDATE
  USING (user_id = current_app_user_id() OR is_privileged())
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());
CREATE POLICY event_rsvps_delete_own ON event_rsvps FOR DELETE
  USING (user_id = current_app_user_id() OR is_privileged());

-- ---- reviews ----------------------------------------------------------------
CREATE TABLE reviews (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id   uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  rating     smallint NOT NULL,
  body       text,
  status     review_status NOT NULL DEFAULT 'published',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz,
  UNIQUE (user_id, event_id),
  CONSTRAINT reviews_rating_chk CHECK (rating BETWEEN 1 AND 5)
);
CREATE INDEX reviews_event_idx ON reviews (event_id, created_at DESC);
CREATE INDEX reviews_user_idx ON reviews (user_id);

ALTER TABLE reviews ENABLE ROW LEVEL SECURITY;
-- Public read = published, non-deleted; owners can still see (and edit) their
-- own rows even after a soft delete, which keeps UPDATE-based soft delete
-- legal under RLS. Privileged roles see everything for moderation.
CREATE POLICY reviews_select ON reviews FOR SELECT
  USING (status = 'published' OR user_id = current_app_user_id() OR is_privileged());
CREATE POLICY reviews_insert_own ON reviews FOR INSERT
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());
CREATE POLICY reviews_update_own ON reviews FOR UPDATE
  USING (user_id = current_app_user_id() OR is_privileged())
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());

-- ---- reports ----------------------------------------------------------------
CREATE TABLE reports (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  reporter_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  entity_type      report_entity_type NOT NULL,
  entity_id        uuid NOT NULL,
  reason_code      text NOT NULL,
  description      text,
  status           report_status NOT NULL DEFAULT 'open',
  assigned_to      uuid REFERENCES users(id) ON DELETE SET NULL,
  resolution       text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  resolved_at      timestamptz
);
CREATE INDEX reports_queue_idx ON reports (status, created_at);
CREATE INDEX reports_target_idx ON reports (entity_type, entity_id, created_at);

ALTER TABLE reports ENABLE ROW LEVEL SECURITY;
CREATE POLICY reports_select_own ON reports FOR SELECT
  USING (reporter_user_id = current_app_user_id() OR is_privileged());
CREATE POLICY reports_insert_own ON reports FOR INSERT
  WITH CHECK (reporter_user_id = current_app_user_id() OR is_privileged());
-- Only admins resolve/assign reports.
CREATE POLICY reports_update_admin ON reports FOR UPDATE
  USING (is_privileged()) WITH CHECK (is_privileged());

-- Reporting a target transitions it to 'under_review' WITHOUT hiding content.
-- Runs as the table owner because the reporter may not own the target (RLS
-- would block the UPDATE); only the transition to under_review is allowed.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION mark_target_reported(p_entity_type text, p_entity_id uuid)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
BEGIN
  CASE p_entity_type
    WHEN 'event'    THEN UPDATE events   SET moderation_status = 'under_review', updated_at = now() WHERE id = p_entity_id;
    WHEN 'venue'    THEN UPDATE venues   SET moderation_status = 'under_review', updated_at = now() WHERE id = p_entity_id;
    WHEN 'user'     THEN UPDATE users    SET moderation_status = 'under_review', updated_at = now() WHERE id = p_entity_id;
    WHEN 'comment'  THEN UPDATE comments SET moderation_status = 'under_review', updated_at = now() WHERE id = p_entity_id;
    WHEN 'organizer' THEN UPDATE organizers SET updated_at = now() WHERE id = p_entity_id;
    ELSE RAISE EXCEPTION 'mark_target_reported: unknown entity type %', p_entity_type;
  END CASE;
END;
$$;
-- +goose StatementEnd

GRANT SELECT, INSERT, UPDATE, DELETE ON event_rsvps, reviews TO app_user;
GRANT SELECT, INSERT ON reports TO app_user;
GRANT EXECUTE ON FUNCTION attempt_event_rsvp(uuid, uuid, boolean) TO app_user;
GRANT EXECUTE ON FUNCTION mark_target_reported(text, uuid) TO app_user;

-- +goose Down
DROP FUNCTION IF EXISTS mark_target_reported(text, uuid);
DROP FUNCTION IF EXISTS attempt_event_rsvp(uuid, uuid, boolean);
DROP TABLE IF EXISTS reports;
DROP TABLE IF EXISTS reviews;
DROP TABLE IF EXISTS event_rsvps;
ALTER TABLE venues DROP COLUMN IF EXISTS moderation_status;
ALTER TABLE users DROP COLUMN IF EXISTS moderation_status;
DROP TYPE IF EXISTS report_status;
DROP TYPE IF EXISTS report_entity_type;
DROP TYPE IF EXISTS review_status;
DROP TYPE IF EXISTS rsvp_status;