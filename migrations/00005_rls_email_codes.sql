-- +goose Up
-- email_codes needs an INSERT policy: verification codes are created during
-- registration/login flows which run under the trusted 'service' role.
CREATE POLICY email_codes_insert ON email_codes FOR INSERT
  WITH CHECK (true);

-- +goose Down
DROP POLICY IF EXISTS email_codes_insert ON email_codes;