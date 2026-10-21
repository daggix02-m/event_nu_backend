-- +goose Up
-- Transactional outbox for domain events. The guarantee the table buys:
-- emitting a fact can no longer race or silently drop, because the INSERT
-- happens in the SAME transaction that changed the aggregate — commit makes
-- the state visible, and the fact exists then too, or neither does.
--
-- The relay worker claims rows with FOR UPDATE SKIP LOCKED + a next_retry_at
-- lease (mirroring email_outbox), publishes to Kafka, and marks rows sent.
-- Delivery is therefore at-least-once: consumers dedupe on (partition, id)
-- or the fact must be idempotent to apply.

CREATE TABLE domain_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    key text NOT NULL,
    payload jsonb NOT NULL,
    owner_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    occurred_at timestamptz NOT NULL,
    produced_at timestamptz NOT NULL DEFAULT now(),
    status text NOT NULL DEFAULT 'pending',
    next_retry_at timestamptz NOT NULL DEFAULT now(),
    attempts int NOT NULL DEFAULT 0,
    sent_at timestamptz
);

CREATE INDEX domain_events_relay_idx ON domain_events (status, next_retry_at);
CREATE INDEX domain_events_key_idx ON domain_events (key);

ALTER TABLE domain_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY domain_events_own ON domain_events FOR ALL
  USING (owner_user_id = current_app_user_id() OR is_privileged())
  WITH CHECK (owner_user_id = current_app_user_id() OR is_privileged());

GRANT SELECT, INSERT, UPDATE ON domain_events TO app_user;

-- +goose Down
DROP TABLE IF EXISTS domain_events;
