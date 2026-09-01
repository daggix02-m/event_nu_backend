-- +goose Up
CREATE TYPE organizer_application_status AS ENUM ('pending', 'approved', 'rejected', 'needs_more_information', 'withdrawn');
CREATE TYPE organizer_status AS ENUM ('active', 'suspended', 'archived');
CREATE TYPE venue_status AS ENUM ('active', 'under_review', 'disabled', 'archived');
CREATE TYPE event_status AS ENUM ('draft', 'published', 'cancelled', 'completed', 'archived');
CREATE TYPE moderation_status AS ENUM ('clean', 'reported', 'under_review', 'blocked', 'restored');
CREATE TYPE event_action_type AS ENUM ('open_entry', 'reservation', 'external_link', 'contact');
CREATE TYPE featured_section AS ENUM ('editors_choice', 'trending', 'new_and_noteworthy');

CREATE TABLE organizer_applications (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  requested_name text NOT NULL,
  requested_slug text,
  bio text,
  supporting_data jsonb NOT NULL DEFAULT '{}',
  status organizer_application_status NOT NULL DEFAULT 'pending',
  reviewed_by uuid REFERENCES users(id) ON DELETE SET NULL,
  reviewed_at timestamptz,
  review_notes text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE organizers (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  slug text NOT NULL UNIQUE,
  name text NOT NULL,
  bio text,
  logo_media_id uuid,
  profile jsonb NOT NULL DEFAULT '{}',
  status organizer_status NOT NULL DEFAULT 'active',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz
);

CREATE TABLE categories (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  slug text NOT NULL UNIQUE,
  name text NOT NULL,
  sort_order integer NOT NULL DEFAULT 0,
  is_active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE venues (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organizer_id uuid REFERENCES organizers(id) ON DELETE RESTRICT,
  name text NOT NULL,
  address text,
  latitude double precision NOT NULL,
  longitude double precision NOT NULL,
  place_id text,
  city text,
  country_code text,
  metadata jsonb NOT NULL DEFAULT '{}',
  status venue_status NOT NULL DEFAULT 'active',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz,
  CONSTRAINT venues_latitude_chk CHECK (latitude BETWEEN -90 AND 90),
  CONSTRAINT venues_longitude_chk CHECK (longitude BETWEEN -180 AND 180)
);

CREATE TABLE events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organizer_id uuid NOT NULL REFERENCES organizers(id) ON DELETE RESTRICT,
  venue_id uuid REFERENCES venues(id) ON DELETE SET NULL,
  category_id uuid REFERENCES categories(id) ON DELETE SET NULL,
  title text NOT NULL,
  description text,
  starts_at timestamptz NOT NULL,
  ends_at timestamptz,
  price_is_free boolean NOT NULL DEFAULT false,
  price_display text,
  action_type event_action_type NOT NULL DEFAULT 'open_entry',
  action_target text,
  status event_status NOT NULL DEFAULT 'draft',
  moderation_status moderation_status NOT NULL DEFAULT 'clean',
  featured featured_section,
  max_attendees integer,
  search_vector tsvector GENERATED ALWAYS AS (
    to_tsvector('english', coalesce(title, '') || ' ' || coalesce(description, ''))
  ) STORED,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz,
  CONSTRAINT events_end_after_start_chk CHECK (ends_at IS NULL OR ends_at >= starts_at),
  CONSTRAINT events_max_attendees_chk CHECK (max_attendees IS NULL OR max_attendees > 0)
);

CREATE INDEX organizer_applications_user_idx ON organizer_applications (user_id);
CREATE INDEX organizer_applications_status_idx ON organizer_applications (status);
CREATE INDEX organizers_owner_idx ON organizers (owner_user_id);
CREATE INDEX venues_organizer_idx ON venues (organizer_id);
CREATE INDEX venues_city_idx ON venues (city);
CREATE INDEX events_organizer_idx ON events (organizer_id);
CREATE INDEX events_venue_idx ON events (venue_id);
CREATE INDEX events_category_idx ON events (category_id);
CREATE INDEX events_published_idx ON events (status, moderation_status, deleted_at);

-- ---- RLS: organizer_applications --------------------------------------
ALTER TABLE organizer_applications ENABLE ROW LEVEL SECURITY;
CREATE POLICY organizer_applications_own ON organizer_applications FOR ALL
  USING (user_id = current_app_user_id() OR is_privileged())
  WITH CHECK (user_id = current_app_user_id() OR is_privileged());

-- ---- RLS: organizers ---------------------------------------------------
ALTER TABLE organizers ENABLE ROW LEVEL SECURITY;
CREATE POLICY organizers_own ON organizers FOR ALL
  USING (owner_user_id = current_app_user_id() OR is_privileged())
  WITH CHECK (owner_user_id = current_app_user_id() OR is_privileged());

-- ---- RLS: categories (public read, admin write) -------------------------
ALTER TABLE categories ENABLE ROW LEVEL SECURITY;
CREATE POLICY categories_read_public ON categories FOR SELECT
  USING (is_active OR is_privileged());
CREATE POLICY categories_insert_privileged ON categories FOR INSERT
  WITH CHECK (is_privileged());
CREATE POLICY categories_update_privileged ON categories FOR UPDATE
  USING (is_privileged())
  WITH CHECK (is_privileged());
CREATE POLICY categories_delete_privileged ON categories FOR DELETE
  USING (is_privileged());

-- ---- RLS: venues --------------------------------------------------------
ALTER TABLE venues ENABLE ROW LEVEL SECURITY;
-- Public: active, non-deleted venues are readable by everyone.
CREATE POLICY venues_read_public ON venues FOR SELECT
  USING ((status = 'active' AND deleted_at IS NULL) OR is_privileged());
-- Writers: the organizer must belong to the authenticated user.
CREATE POLICY venues_insert_own ON venues FOR INSERT
  WITH CHECK (organizer_id IS NULL OR organizer_id IN (
    SELECT id FROM organizers WHERE owner_user_id = current_app_user_id() OR is_privileged()));
CREATE POLICY venues_update_own ON venues FOR UPDATE
  USING (organizer_id IN (SELECT id FROM organizers WHERE owner_user_id = current_app_user_id()) OR is_privileged())
  WITH CHECK (organizer_id IN (SELECT id FROM organizers WHERE owner_user_id = current_app_user_id()) OR is_privileged());

-- ---- RLS: events ---------------------------------------------------------
ALTER TABLE events ENABLE ROW LEVEL SECURITY;
-- Public: published, non-blocked, non-deleted events.
CREATE POLICY events_read_public ON events FOR SELECT
  USING ((status = 'published' AND moderation_status <> 'blocked' AND deleted_at IS NULL) OR is_privileged());
-- Writers: organizer ownership enforced via the owning organizer's user.
CREATE POLICY events_insert_own ON events FOR INSERT
  WITH CHECK (organizer_id IN (SELECT id FROM organizers WHERE owner_user_id = current_app_user_id()) OR is_privileged());
CREATE POLICY events_update_own ON events FOR UPDATE
  USING (organizer_id IN (SELECT id FROM organizers WHERE owner_user_id = current_app_user_id()) OR is_privileged())
  WITH CHECK (organizer_id IN (SELECT id FROM organizers WHERE owner_user_id = current_app_user_id()) OR is_privileged());

GRANT SELECT, INSERT, UPDATE, DELETE ON organizer_applications, organizers, venues, events TO app_user;
GRANT SELECT, INSERT, UPDATE ON categories TO app_user;

-- +goose Down
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS venues;
DROP TABLE IF EXISTS categories;
DROP TABLE IF EXISTS organizers;
DROP TABLE IF EXISTS organizer_applications;
DROP TYPE IF EXISTS featured_section;
DROP TYPE IF EXISTS event_action_type;
DROP TYPE IF EXISTS moderation_status;
DROP TYPE IF EXISTS event_status;
DROP TYPE IF EXISTS venue_status;
DROP TYPE IF EXISTS organizer_status;
DROP TYPE IF EXISTS organizer_application_status;