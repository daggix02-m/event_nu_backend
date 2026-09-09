-- +goose Up
-- Comments and likes (Phase 12a). Both are public-read / owner-write under RLS.
-- Comments additionally expose moderation_status so reported/blocked content is
-- filtered out of public reads while staying visible to privileged roles.

CREATE TABLE comments (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id          uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  user_id           uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  body              text NOT NULL,
  moderation_status moderation_status NOT NULL DEFAULT 'clean',
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  deleted_at        timestamptz,
  CONSTRAINT comments_body_chk CHECK (length(body) BETWEEN 1 AND 2000)
);

CREATE INDEX comments_event_idx ON comments (event_id, created_at DESC);
CREATE INDEX comments_user_idx ON comments (user_id);

ALTER TABLE comments ENABLE ROW LEVEL SECURITY;
-- Ordinary users see only non-blocked, non-deleted comments; privileged roles
-- (admin/service) see everything so moderation stays possible.
CREATE POLICY comments_read_public ON comments FOR SELECT
  USING ((moderation_status <> 'blocked' AND deleted_at IS NULL) OR is_privileged());
CREATE POLICY comments_write_own ON comments FOR INSERT
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());
CREATE POLICY comments_modify_own ON comments FOR UPDATE
  USING (user_id = current_app_user_id() OR is_privileged())
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());
-- Comments are hard-deleted (no soft-delete): Postgres rejects an UPDATE that
-- would make a row invisible to every SELECT policy, so the "effective delete"
-- of a soft-delete would violate comments_read_public for non-privileged rows.
-- Ownership is enforced here; the moderator path uses is_privileged().
CREATE POLICY comments_delete_own ON comments FOR DELETE
  USING (user_id = current_app_user_id() OR is_privileged());

CREATE TABLE likes (
  event_id   uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (event_id, user_id)
);

CREATE INDEX likes_user_idx ON likes (user_id);

ALTER TABLE likes ENABLE ROW LEVEL SECURITY;
CREATE POLICY likes_read_public ON likes FOR SELECT USING (true);
CREATE POLICY likes_insert_own ON likes FOR INSERT
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());
CREATE POLICY likes_delete_own ON likes FOR DELETE
  USING (user_id = current_app_user_id() OR is_privileged());

GRANT SELECT, INSERT, UPDATE, DELETE ON comments TO app_user;
GRANT SELECT, INSERT, DELETE ON likes TO app_user;

-- +goose Down
DROP TABLE IF EXISTS likes;
DROP TABLE IF EXISTS comments;