-- Bootstraps the non-superuser app_user role used by the integration tests,
-- plus the default privileges migrations rely on for RLS-guarded access.
-- Mirrors the bootstrap step in .github/workflows/ci.yml; wrapped in a DO
-- block so re-runs (e.g. `make db-up` after the role already exists) are safe.
--
-- Why app_user must be non-superuser: RLS is bypassed for superusers, so the
-- cross-user-block proof tests would silently pass for the wrong reason. The
-- ALTER DEFAULT PRIVILEGES grants apply to every table/sequence the goose
-- migrations create after this script runs.
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'app_user') THEN
    CREATE ROLE app_user LOGIN PASSWORD 'app_user_pass';
    GRANT CONNECT ON DATABASE event_nu_test TO app_user;
    GRANT USAGE ON SCHEMA public TO app_user;
  END IF;
END
$$;

ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_user;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO app_user;