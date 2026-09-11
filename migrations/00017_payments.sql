-- +goose Up
-- Payments ledger (Phase 15). ONE ledger row per provider transaction —
-- UNIQUE (provider, provider_payment_id) — recording the authoritative money
-- state that the Chapa webhook writes. Writes are provider-driver: the card-init
-- flow (a normal user request, app.role='user') may only OPEN a pending row via
-- the SECURITY DEFINER record_pending_payment() helper; every later transition
-- arrives through the /webhooks/payments/chapa endpoint running under the
-- privileged 'service' role inside its request transaction.
--
-- RLS notes / deviations from the target schema:
--  * Table grants follow the Phase-14 pattern: app_user gets SELECT/INSERT/UPDATE
--    at the privilege level, and RLS policies do the real gating. The webhook
--    connects as the app_user PG role with app.role='service' (is_privileged()),
--    so INSERT/UPDATE pass RLS; regular users are read-only via payments_select.
--  * record_pending_payment() is SECURITY DEFINER (like reserve_ticket_inventory)
--    so the purchasing user can open a ledger row for their own fresh order
--    without holding table write privileges.
--  * Orders carry no payment_status column (per target): the payments row is the
--    status ledger; orders.status still reflects the user-visible outcome.

CREATE TYPE payment_status AS ENUM ('pending', 'authorized', 'paid', 'failed', 'refunded', 'cancelled');

CREATE TABLE payments (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id uuid NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
  provider text NOT NULL,
  provider_payment_id text NOT NULL,
  status payment_status NOT NULL DEFAULT 'pending',
  amount_minor bigint NOT NULL,
  currency char(3) NOT NULL,
  raw_reference jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  paid_at timestamptz,
  failed_at timestamptz,
  UNIQUE (provider, provider_payment_id),
  CONSTRAINT payments_amount_chk CHECK (amount_minor >= 0)
);
CREATE INDEX payments_order_idx ON payments (order_id);

-- Currency consistency: a payment must be denominated in the order's currency.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_payment_currency() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
DECLARE
  v_order_currency char(3);
BEGIN
  SELECT currency INTO v_order_currency FROM orders WHERE id = NEW.order_id;

  IF v_order_currency IS NULL THEN
    RAISE EXCEPTION 'enforce_payment_currency: referenced order not found';
  END IF;

  IF NEW.currency IS DISTINCT FROM v_order_currency THEN
    RAISE EXCEPTION 'payment currency % does not match order currency %', NEW.currency, v_order_currency;
  END IF;

  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER payments_currency_chk
  BEFORE INSERT OR UPDATE OF currency, order_id ON payments
  FOR EACH ROW
  EXECUTE FUNCTION enforce_payment_currency();

-- Open the pending ledger row during card-init. SECURITY DEFINER because the
-- purchasing user may not INSERT payments directly (payments_write requires a
-- privileged role) but must be able to record that a provider checkout started.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_pending_payment(
  p_order_id uuid,
  p_provider text,
  p_provider_payment_id text,
  p_amount_minor bigint,
  p_currency char(3)
) RETURNS uuid
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
DECLARE
  v_id uuid;
BEGIN
  INSERT INTO payments (order_id, provider, provider_payment_id, amount_minor, currency)
  VALUES (p_order_id, p_provider, p_provider_payment_id, p_amount_minor, p_currency)
  RETURNING id INTO v_id;

  RETURN v_id;
END;
$$;
-- +goose StatementEnd

-- ---- RLS ---------------------------------------------------------------------
ALTER TABLE payments ENABLE ROW LEVEL SECURITY;
-- Regular users see only their own orders' payment rows; privileged roles see
-- everything.
CREATE POLICY payments_select ON payments FOR SELECT
  USING (
    EXISTS (SELECT 1 FROM orders o WHERE o.id = payments.order_id AND o.user_id = current_app_user_id())
    OR is_privileged()
  );
-- Writes enter through the webhook (privileged role) or the SECURITY DEFINER
-- record_pending_payment() helper — never directly by a regular user.
CREATE POLICY payments_write ON payments FOR INSERT
  WITH CHECK (is_privileged());
CREATE POLICY payments_update ON payments FOR UPDATE
  USING (is_privileged()) WITH CHECK (is_privileged());

-- ---- GRANTs ------------------------------------------------------------------
GRANT SELECT, INSERT, UPDATE ON payments TO app_user, admin_dashboard, worker_role;
GRANT EXECUTE ON FUNCTION record_pending_payment(uuid, text, text, bigint, char) TO app_user, worker_role;
GRANT EXECUTE ON FUNCTION enforce_payment_currency() TO PUBLIC;

-- +goose Down
DROP TRIGGER IF EXISTS payments_currency_chk ON payments;
DROP TABLE IF EXISTS payments;
DROP FUNCTION IF EXISTS record_pending_payment(uuid, text, text, bigint, char);
DROP FUNCTION IF EXISTS enforce_payment_currency();
DROP TYPE IF EXISTS payment_status;