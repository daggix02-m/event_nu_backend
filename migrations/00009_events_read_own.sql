-- +goose Up
-- Owners must be able to see their own events (drafts included) — needed for
-- INSERT...RETURNING and for editing drafts. Postgres ORs multiple SELECT
-- policies, so public readers still see only published rows.
CREATE POLICY events_read_own ON events FOR SELECT
  USING (organizer_id IN (SELECT id FROM organizers WHERE owner_user_id = current_app_user_id()) OR is_privileged());

-- +goose Down
DROP POLICY IF EXISTS events_read_own ON events;