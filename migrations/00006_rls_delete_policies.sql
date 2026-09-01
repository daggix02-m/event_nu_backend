-- +goose Up
-- DELETE policies for the trusted 'service' role: the worker needs to prune
-- processed outbox rows and codes; tests purge these tables for isolation.
CREATE POLICY email_outbox_delete_privileged ON email_outbox FOR DELETE
  USING (is_privileged());
CREATE POLICY email_logs_delete_privileged ON email_logs FOR DELETE
  USING (is_privileged());
CREATE POLICY email_codes_delete_privileged ON email_codes FOR DELETE
  USING (is_privileged());

-- +goose Down
DROP POLICY IF EXISTS email_outbox_delete_privileged ON email_outbox;
DROP POLICY IF EXISTS email_logs_delete_privileged ON email_logs;
DROP POLICY IF EXISTS email_codes_delete_privileged ON email_codes;