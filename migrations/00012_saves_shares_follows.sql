-- +goose Up
-- Saves/save-folders, share analytics, and organizer follows (Phase 12b).
-- Saves and folders are strictly personal ("own rows" RLS); shares are a
-- per-user analytics append (deduplicated by user+event+channel+ref so a
-- double-tap on the share button records one row); organizer_follows reads are
-- public so follower counts and follow state can be served to any reader.
-- Organizers gain a public-read policy so profiles are visible to all (a
-- suspended/archived organizer still hides its row from non-privileged reads).

-- ---- save_folders -------------------------------------------------------
CREATE TABLE save_folders (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT save_folders_name_chk CHECK (length(btrim(name)) BETWEEN 1 AND 100)
);
CREATE INDEX save_folders_user_idx ON save_folders (user_id);

-- ---- saves ----------------------------------------------------------------
CREATE TABLE saves (
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  event_id   uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  folder_id  uuid REFERENCES save_folders(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, event_id)
);
CREATE INDEX saves_folder_idx ON saves (folder_id);

-- ---- shares ---------------------------------------------------------------
-- Analytics row per share action. channel (e.g. "whatsapp") and ref (e.g.
-- "home-feed") are non-null in this iteration so the dedup index is a plain
-- unique constraint; the null-able variant in the target schema is deliberately
-- not reproduced here.
CREATE TABLE shares (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  event_id   uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  channel    text NOT NULL DEFAULT '',
  ref        text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT shares_dedup_key UNIQUE (user_id, event_id, channel, ref)
);
CREATE INDEX shares_event_idx ON shares (event_id);
CREATE INDEX shares_user_idx ON shares (user_id);

-- ---- organizer_follows ----------------------------------------------------
CREATE TABLE organizer_follows (
  user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  organizer_id uuid NOT NULL REFERENCES organizers(id) ON DELETE CASCADE,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, organizer_id)
);
CREATE INDEX organizer_follows_organizer_idx ON organizer_follows (organizer_id);

-- ---- RLS ------------------------------------------------------------
ALTER TABLE save_folders ENABLE ROW LEVEL SECURITY;
CREATE POLICY save_folders_own ON save_folders FOR ALL
  USING (user_id = current_app_user_id() OR is_privileged())
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());

ALTER TABLE saves ENABLE ROW LEVEL SECURITY;
CREATE POLICY saves_own ON saves FOR ALL
  USING (user_id = current_app_user_id() OR is_privileged())
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());

ALTER TABLE shares ENABLE ROW LEVEL SECURITY;
CREATE POLICY shares_read_own ON shares FOR SELECT
  USING (user_id = current_app_user_id() OR is_privileged());
CREATE POLICY shares_write_own ON shares FOR INSERT
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());

ALTER TABLE organizer_follows ENABLE ROW LEVEL SECURITY;
-- Public reads: follower counts and "who follows whom" are public data, and
-- aggregate COUNT(*) must be servable to every reader (see target schema's RLS
-- note about aggregate counts). Writes remain own-row only.
CREATE POLICY organizer_follows_read_public ON organizer_follows FOR SELECT USING (true);
CREATE POLICY organizer_follows_write_own ON organizer_follows FOR INSERT
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());
CREATE POLICY organizer_follows_delete_own ON organizer_follows FOR DELETE
  USING (user_id = current_app_user_id() OR is_privileged());

-- Active organizers are public profiles; owners/privileged roles still see all.
CREATE POLICY organizers_read_public ON organizers FOR SELECT
  USING ((status = 'active' AND deleted_at IS NULL) OR is_privileged());

GRANT SELECT, INSERT, UPDATE, DELETE ON save_folders, saves TO app_user;
GRANT SELECT, INSERT ON shares TO app_user;
GRANT SELECT, INSERT, DELETE ON organizer_follows TO app_user;

-- +goose Down
DROP POLICY IF EXISTS organizers_read_public ON organizers;
DROP TABLE IF EXISTS organizer_follows;
DROP TABLE IF EXISTS shares;
DROP TABLE IF EXISTS saves;
DROP TABLE IF EXISTS save_folders;