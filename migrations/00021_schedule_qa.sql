-- +goose Up
-- Schedule & Q&A (Phase 19). Event sessions for multi-track scheduling and an
-- audience Q&A system with upvoting and organizer pinning/answering.

-- ---- event_sessions --------------------------------------------------------
-- Schedule slots belonging to an event. Organizers manage CRUD; public readers
-- see only published events' sessions.

CREATE TABLE event_sessions (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id   uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  title      text NOT NULL,
  speaker    text,
  stage      text,
  starts_at  timestamptz NOT NULL,
  ends_at    timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT event_sessions_title_chk CHECK (length(title) BETWEEN 1 AND 200),
  CONSTRAINT event_sessions_end_after_start_chk CHECK (ends_at IS NULL OR ends_at >= starts_at)
);

CREATE INDEX event_sessions_event_idx ON event_sessions (event_id, starts_at);

-- ---- RLS: event_sessions --------------------------------------------------
ALTER TABLE event_sessions ENABLE ROW LEVEL SECURITY;

-- SELECT: public reads published events; organizer can see own (incl. drafts).
CREATE POLICY event_sessions_select ON event_sessions FOR SELECT
  USING (
    is_privileged()
    OR event_id IN (
      SELECT id FROM events
      WHERE (status = 'published' AND moderation_status <> 'blocked' AND deleted_at IS NULL)
         OR organizer_id IN (SELECT id FROM organizers WHERE owner_user_id = current_app_user_id())
    )
  );

-- INSERT/UPDATE/DELETE: only event organizer or privileged.
CREATE POLICY event_sessions_insert ON event_sessions FOR INSERT
  WITH CHECK (
    is_privileged()
    OR event_id IN (
      SELECT id FROM events WHERE organizer_id IN (
        SELECT id FROM organizers WHERE owner_user_id = current_app_user_id()
      )
    )
  );

CREATE POLICY event_sessions_update ON event_sessions FOR UPDATE
  USING (
    is_privileged()
    OR event_id IN (
      SELECT id FROM events WHERE organizer_id IN (
        SELECT id FROM organizers WHERE owner_user_id = current_app_user_id()
      )
    )
  )
  WITH CHECK (
    is_privileged()
    OR event_id IN (
      SELECT id FROM events WHERE organizer_id IN (
        SELECT id FROM organizers WHERE owner_user_id = current_app_user_id()
      )
    )
  );

CREATE POLICY event_sessions_delete ON event_sessions FOR DELETE
  USING (
    is_privileged()
    OR event_id IN (
      SELECT id FROM events WHERE organizer_id IN (
        SELECT id FROM organizers WHERE owner_user_id = current_app_user_id()
      )
    )
  );

GRANT SELECT, INSERT, UPDATE, DELETE ON event_sessions TO app_user;

-- ---- event_questions -------------------------------------------------------
-- Audience Q&A: signed-in users post questions on published events.
-- Organizers pin and answer.

CREATE TABLE event_questions (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id   uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  body       text NOT NULL,
  pinned     boolean NOT NULL DEFAULT false,
  answer     text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT event_questions_body_chk CHECK (length(body) BETWEEN 1 AND 2000)
);

CREATE INDEX event_questions_event_idx ON event_questions (event_id, pinned DESC, created_at ASC);

-- ---- RLS: event_questions -------------------------------------------------
ALTER TABLE event_questions ENABLE ROW LEVEL SECURITY;

-- SELECT: public for published events (same gating as sessions moments).
CREATE POLICY event_questions_select ON event_questions FOR SELECT
  USING (
    is_privileged()
    OR EXISTS (
      SELECT 1 FROM events e
      WHERE e.id = event_questions.event_id
        AND e.status = 'published'
        AND e.moderation_status <> 'blocked'
        AND e.deleted_at IS NULL
    )
  );

-- INSERT: author only.
CREATE POLICY event_questions_insert ON event_questions FOR INSERT
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());

-- UPDATE: author OR event organizer (pin/answer) OR privileged.
CREATE POLICY event_questions_update ON event_questions FOR UPDATE
  USING (
    user_id = current_app_user_id()
    OR is_privileged()
    OR event_id IN (
      SELECT id FROM events WHERE organizer_id IN (
        SELECT id FROM organizers WHERE owner_user_id = current_app_user_id()
      )
    )
  )
  WITH CHECK (
    user_id = current_app_user_id()
    OR is_privileged()
    OR event_id IN (
      SELECT id FROM events WHERE organizer_id IN (
        SELECT id FROM organizers WHERE owner_user_id = current_app_user_id()
      )
    )
  );

GRANT SELECT, INSERT, UPDATE ON event_questions TO app_user;

-- ---- event_question_votes --------------------------------------------------
-- Upvotes on questions. Unique (question_id, user_id) makes add/remove
-- idempotent via ON CONFLICT DO NOTHING / DELETE.

CREATE TABLE event_question_votes (
  question_id uuid NOT NULL REFERENCES event_questions(id) ON DELETE CASCADE,
  user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (question_id, user_id)
);

CREATE INDEX event_question_votes_user_idx ON event_question_votes (user_id);

-- ---- RLS: event_question_votes --------------------------------------------
ALTER TABLE event_question_votes ENABLE ROW LEVEL SECURITY;

-- SELECT: public (vote counts shown to everyone).
CREATE POLICY event_question_votes_select ON event_question_votes FOR SELECT USING (true);

-- INSERT/DELETE: own votes only.
CREATE POLICY event_question_votes_insert ON event_question_votes FOR INSERT
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());
CREATE POLICY event_question_votes_delete ON event_question_votes FOR DELETE
  USING (user_id = current_app_user_id() OR is_privileged());

GRANT SELECT, INSERT, DELETE ON event_question_votes TO app_user;

-- +goose Down
DROP TABLE IF EXISTS event_question_votes;
DROP TABLE IF EXISTS event_questions;
DROP TABLE IF EXISTS event_sessions;
