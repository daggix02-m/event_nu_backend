-- +goose Up
-- Add the honest publish timestamp the admin Overview's per-day
-- events_published series needs. events carries no publish history and no
-- published_at column today, so 'events published per day' would otherwise
-- have to be fabricated from status/created_at. This column is the real one.
--
-- Backfill: every row already at status='published' gets published_at =
-- created_at. This is a LABELED APPROXIMATION — the actual publish moment
-- predates this migration and cannot be recovered, so a backfill equal to
-- creation is the honest upper bound we can offer; the client labels the
-- earliest days accordingly. New publishes stamp the real timestamp.
--
-- The column is added nullable and left that way: rows still in draft must not
-- carry a publish instant, so NULL is the meaningful value rather than a
-- default.

ALTER TABLE events ADD COLUMN published_at timestamptz;

UPDATE events
SET published_at = created_at
WHERE status = 'published';

-- Index the admin Overview's per-day published rollup, which filters to
-- published rows and buckets on published_at.
CREATE INDEX events_published_at_idx
  ON events (published_at)
  WHERE published_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS events_published_at_idx;
ALTER TABLE events DROP COLUMN IF EXISTS published_at;
