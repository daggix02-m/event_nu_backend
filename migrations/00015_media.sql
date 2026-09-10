-- +goose Up
-- Media pipeline: assets, variant outputs, and processing jobs. Models the
-- target schema (media_assets / media_variants / media_processing_jobs) with
-- two documented deviations:
--   1. media_assets gains `deleted_at` (target has none) so DELETE /media/{id}
--      is a soft delete; ready-public reads exclude deleted rows.
--   2. app_user is granted DELETE on media_assets/media_variants (target
--      omitted it) because soft-delete + the worker's variant cleanup need it;
--      row access is still pinned by RLS (own rows / privileged only).

CREATE TYPE media_kind AS ENUM ('event_poster', 'event_teaser', 'event_gallery', 'organizer_logo', 'avatar', 'story');
CREATE TYPE media_status AS ENUM ('pending', 'processing', 'ready', 'failed');

CREATE TABLE media_assets (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  uploader_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  kind media_kind NOT NULL,
  storage_bucket text NOT NULL,
  storage_key text NOT NULL,
  content_type text NOT NULL,
  byte_size bigint,
  width integer,
  height integer,
  blurhash text,
  status media_status NOT NULL DEFAULT 'pending',
  error_message text,
  deleted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE media_variants (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  media_asset_id uuid NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
  variant text NOT NULL,
  density text NOT NULL DEFAULT '1x',
  format text NOT NULL,
  width integer NOT NULL,
  height integer NOT NULL,
  storage_key text NOT NULL,
  cdn_url text NOT NULL,
  byte_size bigint,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (media_asset_id, variant, density, format)
);

CREATE TABLE media_processing_jobs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  media_asset_id uuid NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
  status text NOT NULL DEFAULT 'queued',
  attempts integer NOT NULL DEFAULT 0,
  -- Lease + retry scheduling (deviates from the target schema, which lacks a
  -- backoff column): a claimed job is stamped with next_retry_at so a crashed
  -- worker re-queues it after the lease expires and a failed job retries after
  -- its backoff delay. Mirrors the email_outbox pattern (00003).
  next_retry_at timestamptz NOT NULL DEFAULT now(),
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX media_assets_uploader_idx ON media_assets(uploader_id);
CREATE INDEX media_assets_status_idx ON media_assets(status);
CREATE INDEX media_variants_asset_idx ON media_variants(media_asset_id);
CREATE INDEX media_variants_lookup_idx ON media_variants(media_asset_id, variant);
CREATE INDEX media_jobs_status_idx ON media_processing_jobs(status, next_retry_at);
CREATE UNIQUE INDEX media_jobs_asset_unique ON media_processing_jobs(media_asset_id);

-- The poster/teaser columns were added (nullable, FK-less) in 00014; the media
-- table now exists, so pin the FKs with ON DELETE SET NULL (target schema).
ALTER TABLE events
  ADD CONSTRAINT events_poster_media_fk
  FOREIGN KEY (poster_media_id) REFERENCES media_assets(id) ON DELETE SET NULL;
ALTER TABLE events
  ADD CONSTRAINT events_teaser_media_fk
  FOREIGN KEY (teaser_media_id) REFERENCES media_assets(id) ON DELETE SET NULL;

-- ---- media_assets ----
ALTER TABLE media_assets ENABLE ROW LEVEL SECURITY;
GRANT SELECT, INSERT, UPDATE, DELETE ON media_assets TO app_user, admin_dashboard, worker_role;
-- app_user also owns uploads, so it needs DELETE for soft-deletes (deviation 2).

CREATE POLICY media_assets_select_own ON media_assets FOR SELECT
  USING (uploader_id = current_app_user_id() OR is_privileged());
-- Processed media is meant to be publicly displayed (posters, logos, avatars,
-- gallery): anyone can read `ready` rows once the CDN/storage window opens.
-- RLS on this table is NOT the "should this poster be shown yet" gate — the
-- owning entity's own visibility checks are (events draft/published etc).
-- Deleted rows are excluded from the public read.
CREATE POLICY media_assets_select_ready ON media_assets FOR SELECT
  USING (status = 'ready' AND deleted_at IS NULL);
CREATE POLICY media_assets_insert ON media_assets FOR INSERT
  WITH CHECK (uploader_id = current_app_user_id() OR is_privileged());
CREATE POLICY media_assets_update ON media_assets FOR UPDATE
  USING (uploader_id = current_app_user_id() OR is_privileged())
  WITH CHECK (uploader_id = current_app_user_id() OR is_privileged());
CREATE POLICY media_assets_delete ON media_assets FOR DELETE
  USING (uploader_id = current_app_user_id() OR is_privileged());

-- ---- media_variants ----
ALTER TABLE media_variants ENABLE ROW LEVEL SECURITY;
GRANT SELECT, INSERT, UPDATE, DELETE ON media_variants TO app_user, admin_dashboard, worker_role;

CREATE POLICY media_variants_select ON media_variants FOR SELECT
  USING (EXISTS (
    SELECT 1 FROM media_assets a
    WHERE a.id = media_variants.media_asset_id
      AND (a.uploader_id = current_app_user_id() OR (a.status = 'ready' AND a.deleted_at IS NULL) OR is_privileged())
  ));
CREATE POLICY media_variants_write ON media_variants FOR INSERT
  WITH CHECK (is_privileged());
CREATE POLICY media_variants_update ON media_variants FOR UPDATE
  USING (is_privileged())
  WITH CHECK (is_privileged());
CREATE POLICY media_variants_delete ON media_variants FOR DELETE
  USING (is_privileged());

-- ---- media_processing_jobs ----
-- Internal to the pipeline: no app-facing ACL beyond the table grants; every
-- policy requires a privileged (service/admin) RLS context, so browsers and
-- plain app_user requests can never see or mutate jobs.
ALTER TABLE media_processing_jobs ENABLE ROW LEVEL SECURITY;
GRANT SELECT, INSERT, UPDATE, DELETE ON media_processing_jobs TO app_user, admin_dashboard, worker_role;

CREATE POLICY media_jobs_privileged ON media_processing_jobs FOR ALL
  USING (is_privileged())
  WITH CHECK (is_privileged());

-- The API enqueues a job after an upload completes, but the job table is
-- privileged-only (browsers must not see/queue jobs). enqueue_media_job is a
-- SECURITY DEFINER function (mirrors notify_user in 00014): it runs as the
-- migration owner, bypassing RLS, and returns void — success of the enqueue is
-- verified by the job row existing when the worker claims it.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enqueue_media_job(p_media_asset_id uuid)
RETURNS void
LANGUAGE sql
SECURITY DEFINER
SET search_path = public
AS $$
  INSERT INTO media_processing_jobs (media_asset_id)
  VALUES (p_media_asset_id)
  ON CONFLICT (media_asset_id) DO NOTHING;
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION enqueue_media_job(uuid) TO app_user, admin_dashboard, worker_role;

-- +goose Down
DROP FUNCTION IF EXISTS enqueue_media_job(uuid);
DROP TABLE IF EXISTS media_processing_jobs, media_variants, media_assets CASCADE;
DROP TYPE IF EXISTS media_status;
DROP TYPE IF EXISTS media_kind;
ALTER TABLE events DROP CONSTRAINT IF EXISTS events_poster_media_fk;
ALTER TABLE events DROP CONSTRAINT IF EXISTS events_teaser_media_fk;