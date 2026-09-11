-- +goose Up
-- Offline sync foundation (Phase 16): privileged soft-delete detector and the
-- indexes that keep the cursor-based delta scans on the hot path.
--
-- * sync_deleted_ids() lets GET /api/v1/sync learn which rows a user can no
--   longer see. Normal SELECTs run as app_user under RLS, which filters
--   soft-deleted rows out (deleted_at IS NULL in every public policy), so the
--   delta query could never observe a deletion. This SECURITY DEFINER helper
--   reads the soft-deleted ids as the function owner and returns only
--   {id, updated_at} — never row content — so a caller learns a row vanished
--   without learning what it said.
-- * The domain whitelist is enforced inside the function AND in Go; the table
--   name only reaches format('%I') after a successful whitelist check, so it
--   is not injectable.
-- * Only tables with soft-delete (deleted_at) support belong in the whitelist.
--   Comments are HARD-deleted (DELETE FROM comments), so they cannot emit
--   delete ops and are intentionally absent.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION sync_deleted_ids(p_since timestamptz, p_domain text, p_limit integer)
RETURNS TABLE (row_id uuid, changed_at timestamptz)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
BEGIN
  IF p_domain NOT IN ('events', 'venues', 'organizers', 'reviews') THEN
    RAISE EXCEPTION 'unsupported sync domain: %', p_domain;
  END IF;
  RETURN QUERY EXECUTE format(
    'SELECT id, updated_at FROM %I WHERE deleted_at IS NOT NULL AND updated_at >= $1 ORDER BY updated_at, id LIMIT $2',
    p_domain
  ) USING p_since, p_limit;
END;
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION sync_deleted_ids(timestamptz, text, integer) TO app_user;

-- ORDER BY updated_at, id sequences for the delta scans.
CREATE INDEX events_sync_idx ON events (updated_at, id) WHERE deleted_at IS NULL;
CREATE INDEX venues_sync_idx ON venues (updated_at, id) WHERE deleted_at IS NULL;
CREATE INDEX organizers_sync_idx ON organizers (updated_at, id) WHERE deleted_at IS NULL;
CREATE INDEX categories_sync_idx ON categories (updated_at, id);
CREATE INDEX ticket_types_sync_idx ON ticket_types (updated_at, id);
CREATE INDEX comments_sync_idx ON comments (updated_at, id) WHERE deleted_at IS NULL;
CREATE INDEX reviews_sync_idx ON reviews (updated_at, id) WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS reviews_sync_idx;
DROP INDEX IF EXISTS comments_sync_idx;
DROP INDEX IF EXISTS ticket_types_sync_idx;
DROP INDEX IF EXISTS categories_sync_idx;
DROP INDEX IF EXISTS organizers_sync_idx;
DROP INDEX IF EXISTS venues_sync_idx;
DROP INDEX IF EXISTS events_sync_idx;

DROP FUNCTION IF EXISTS sync_deleted_ids(timestamptz, text, integer);