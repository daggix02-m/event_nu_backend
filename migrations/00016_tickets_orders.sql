-- +goose Up
-- Tickets & orders (Phase 14). Ticket tiers, order creation with ATOMIC
-- inventory reservation (no oversell under concurrency), order cancellation
-- releasing inventory, and paid orders issuing HMAC-signed QR tickets.
-- Payment capture itself arrives with the Phase 15 Chapa webhook; this phase
-- provides the ConfirmPaid machinery it will call (exercised by tests today).
--
-- RLS notes / deviations from the target schema:
--  * reserve_ticket_inventory / release_ticket_inventory are SECURITY DEFINER
--    (target defined them owned, but ticket_types has RLS and only the event
--    organizer may write it; the purchasing user must not). They are the ONLY
--    sanctioned way to move quantity_sold.
--  * tickets.check-in is exposed via the SECURITY DEFINER check_in_ticket()
--    so the event organizer can mark a ticket used WITHOUT holding table write
--    privileges (tickets_update is privileged-role only, per target).
--  * RLS policies use the codebase's organizer-ownership join pattern
--    (owner_user_id = current_app_user_id()) instead of the target's
--    current_app_organizer_id() helper, which does not exist here.
--  * payments is deferred to Phase 15.

CREATE TYPE order_status AS ENUM ('pending', 'paid', 'failed', 'cancelled', 'refunded');
CREATE TYPE ticket_status AS ENUM ('issued', 'used', 'cancelled', 'refunded');

-- ---- ticket_types -----------------------------------------------------------
CREATE TABLE ticket_types (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  name text NOT NULL,
  description text,
  price_minor bigint NOT NULL,
  currency char(3) NOT NULL,
  quantity_total integer,
  quantity_sold integer NOT NULL DEFAULT 0,
  sales_start timestamptz,
  sales_end timestamptz,
  is_active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT ticket_types_price_chk CHECK (price_minor >= 0),
  CONSTRAINT ticket_types_quantity_chk CHECK (quantity_total IS NULL OR quantity_total >= 0),
  CONSTRAINT ticket_types_sold_chk CHECK (quantity_sold >= 0),
  CONSTRAINT ticket_types_sold_le_total_chk CHECK (quantity_total IS NULL OR quantity_sold <= quantity_total),
  CONSTRAINT ticket_types_sales_window_chk CHECK (sales_end IS NULL OR sales_start IS NULL OR sales_end >= sales_start)
);
CREATE INDEX ticket_types_event_idx ON ticket_types (event_id);

-- Atomic inventory reservation: the Go service calls this inside the order
-- transaction instead of a read-then-write of quantity_sold. Returns true and
-- reserves the quantity when inventory is available (or unlimited); returns
-- false and reserves nothing otherwise. SECURITY DEFINER so the purchasing
-- user (no ticket_types write rights) can still reserve atomically.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reserve_ticket_inventory(
  p_ticket_type_id uuid,
  p_quantity integer
) RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
DECLARE
  v_reserved boolean;
BEGIN
  IF p_quantity <= 0 THEN
    RAISE EXCEPTION 'reserve_ticket_inventory: quantity must be positive';
  END IF;

  UPDATE ticket_types
  SET quantity_sold = quantity_sold + p_quantity,
      updated_at = now()
  WHERE id = p_ticket_type_id
    AND is_active
    AND (quantity_total IS NULL OR quantity_sold + p_quantity <= quantity_total)
  RETURNING true INTO v_reserved;

  RETURN coalesce(v_reserved, false);
END;
$$;
-- +goose StatementEnd

-- Mirror release path for cancellations/refunds prior to the ticket being used.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION release_ticket_inventory(
  p_ticket_type_id uuid,
  p_quantity integer
) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
BEGIN
  IF p_quantity <= 0 THEN
    RAISE EXCEPTION 'release_ticket_inventory: quantity must be positive';
  END IF;

  UPDATE ticket_types
  SET quantity_sold = GREATEST(quantity_sold - p_quantity, 0),
      updated_at = now()
  WHERE id = p_ticket_type_id;
END;
$$;
-- +goose StatementEnd

-- ---- orders / order_items ---------------------------------------------------
CREATE TABLE orders (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
  status order_status NOT NULL DEFAULT 'pending',
  currency char(3) NOT NULL,
  subtotal_minor bigint NOT NULL,
  total_minor bigint NOT NULL,
  payment_provider text,
  provider_ref text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  paid_at timestamptz,
  cancelled_at timestamptz,
  CONSTRAINT orders_amounts_chk CHECK (subtotal_minor >= 0 AND total_minor >= 0)
);
CREATE INDEX orders_user_idx ON orders (user_id, created_at DESC);
CREATE INDEX orders_event_idx ON orders (event_id);

CREATE TABLE order_items (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  ticket_type_id uuid NOT NULL REFERENCES ticket_types(id) ON DELETE RESTRICT,
  quantity integer NOT NULL,
  currency char(3) NOT NULL,
  unit_price bigint NOT NULL,
  subtotal bigint NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT order_items_quantity_chk CHECK (quantity > 0),
  CONSTRAINT order_items_price_chk CHECK (unit_price >= 0 AND subtotal >= 0)
);
CREATE INDEX order_items_order_idx ON order_items (order_id);
CREATE INDEX order_items_type_idx ON order_items (ticket_type_id);

-- Currency consistency across the purchase chain. CHECK constraints cannot
-- reference other tables, so this invariant is enforced with a trigger (the
-- Go service validates the same invariant too — this is the DB backstop).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_order_item_currency() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
DECLARE
  v_order_currency char(3);
  v_ticket_currency char(3);
BEGIN
  SELECT currency INTO v_order_currency FROM orders WHERE id = NEW.order_id;
  SELECT currency INTO v_ticket_currency FROM ticket_types WHERE id = NEW.ticket_type_id;

  IF v_order_currency IS NULL OR v_ticket_currency IS NULL THEN
    RAISE EXCEPTION 'enforce_order_item_currency: referenced order or ticket_type not found';
  END IF;

  IF NEW.currency IS DISTINCT FROM v_order_currency THEN
    RAISE EXCEPTION 'order_item currency % does not match order currency %', NEW.currency, v_order_currency;
  END IF;

  IF NEW.currency IS DISTINCT FROM v_ticket_currency THEN
    RAISE EXCEPTION 'order_item currency % does not match ticket_type currency %', NEW.currency, v_ticket_currency;
  END IF;

  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER order_items_currency_chk
  BEFORE INSERT OR UPDATE OF currency, order_id, ticket_type_id ON order_items
  FOR EACH ROW
  EXECUTE FUNCTION enforce_order_item_currency();

-- ---- tickets ----------------------------------------------------------------
CREATE TABLE tickets (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  order_item_id uuid NOT NULL REFERENCES order_items(id) ON DELETE RESTRICT,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
  ticket_type_id uuid NOT NULL REFERENCES ticket_types(id) ON DELETE RESTRICT,
  code_hash text NOT NULL UNIQUE,
  status ticket_status NOT NULL DEFAULT 'issued',
  issued_at timestamptz NOT NULL DEFAULT now(),
  used_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX tickets_order_item_idx ON tickets (order_item_id);
CREATE INDEX tickets_user_idx ON tickets (user_id, status);
CREATE INDEX tickets_event_idx ON tickets (event_id);

-- Server-authoritative check-in. The organizer scans a QR payload that, once
-- verified, yields a code_hash (sha256 of the canonical payload). This function
-- validates the caller is the event's organizer, then marks the matching ticket
-- used — atomically, single UPDATE, before any read-modify-write is possible.
-- Returns 'ok' | 'already_used' | 'not_found' | 'forbidden'.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_in_ticket(p_code_hash text, p_event_id uuid)
RETURNS text
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
DECLARE
  v_status ticket_status;
  v_uid uuid := current_app_user_id();
BEGIN
  IF v_uid IS NULL OR NOT EXISTS (
    SELECT 1 FROM events e
    WHERE e.id = p_event_id
      AND e.organizer_id IN (
        SELECT id FROM organizers
        WHERE owner_user_id = v_uid OR is_privileged()
      )
  ) THEN
    RETURN 'forbidden';
  END IF;

  UPDATE tickets
  SET status = 'used', used_at = now()
  WHERE code_hash = p_code_hash
    AND event_id = p_event_id
    AND status = 'issued'
  RETURNING status INTO v_status;

  IF FOUND THEN
    RETURN 'ok';
  END IF;

  IF EXISTS (SELECT 1 FROM tickets WHERE code_hash = p_code_hash AND event_id = p_event_id) THEN
    RETURN 'already_used';
  END IF;

  RETURN 'not_found';
END;
$$;
-- +goose StatementEnd

-- ---- RLS ---------------------------------------------------------------------
ALTER TABLE ticket_types ENABLE ROW LEVEL SECURITY;
-- Public read = active tiers of any event; event visibility is already gated
-- by the events RLS read policies (a GET on a hidden event 404s first).
CREATE POLICY ticket_types_select_public ON ticket_types FOR SELECT
  USING (is_active);
CREATE POLICY ticket_types_select_own ON ticket_types FOR SELECT
  USING (EXISTS (
    SELECT 1 FROM events e
    WHERE e.id = ticket_types.event_id
      AND e.organizer_id IN (SELECT id FROM organizers WHERE owner_user_id = current_app_user_id())
  ) OR is_privileged());
CREATE POLICY ticket_types_insert_own ON ticket_types FOR INSERT
  WITH CHECK (EXISTS (
    SELECT 1 FROM events e
    WHERE e.id = ticket_types.event_id
      AND e.organizer_id IN (SELECT id FROM organizers WHERE owner_user_id = current_app_user_id())
  ) OR is_privileged());
CREATE POLICY ticket_types_update_own ON ticket_types FOR UPDATE
  USING (EXISTS (
    SELECT 1 FROM events e
    WHERE e.id = ticket_types.event_id
      AND e.organizer_id IN (SELECT id FROM organizers WHERE owner_user_id = current_app_user_id())
  ) OR is_privileged())
  WITH CHECK (EXISTS (
    SELECT 1 FROM events e
    WHERE e.id = ticket_types.event_id
      AND e.organizer_id IN (SELECT id FROM organizers WHERE owner_user_id = current_app_user_id())
  ) OR is_privileged());

ALTER TABLE orders ENABLE ROW LEVEL SECURITY;
CREATE POLICY orders_select ON orders FOR SELECT
  USING (
    user_id = current_app_user_id()
    OR EXISTS (
      SELECT 1 FROM events e
      WHERE e.id = orders.event_id
        AND e.organizer_id IN (SELECT id FROM organizers WHERE owner_user_id = current_app_user_id())
    )
    OR is_privileged()
  );
CREATE POLICY orders_insert ON orders FOR INSERT
  WITH CHECK (user_id = current_app_user_id());
-- Status transitions (pending -> paid/cancelled/failed/refunded) happen through
-- the worker (privileged) or the order owner cancelling their own pending order.
CREATE POLICY orders_update ON orders FOR UPDATE
  USING (is_privileged() OR user_id = current_app_user_id())
  WITH CHECK (is_privileged() OR user_id = current_app_user_id());

ALTER TABLE order_items ENABLE ROW LEVEL SECURITY;
CREATE POLICY order_items_rw ON order_items FOR ALL
  USING (EXISTS (
    SELECT 1 FROM orders o
    WHERE o.id = order_items.order_id
      AND (o.user_id = current_app_user_id() OR is_privileged())
  ))
  WITH CHECK (EXISTS (
    SELECT 1 FROM orders o
    WHERE o.id = order_items.order_id
      AND (o.user_id = current_app_user_id() OR is_privileged())
  ));

ALTER TABLE tickets ENABLE ROW LEVEL SECURITY;
CREATE POLICY tickets_select ON tickets FOR SELECT
  USING (
    user_id = current_app_user_id()
    OR EXISTS (
      SELECT 1 FROM events e
      WHERE e.id = tickets.event_id
        AND e.organizer_id IN (SELECT id FROM organizers WHERE owner_user_id = current_app_user_id())
    )
    OR is_privileged()
  );
-- Ticket state changes only through check_in_ticket() and the refund worker —
-- never directly by the ticket holder.
CREATE POLICY tickets_insert_privileged ON tickets FOR INSERT
  WITH CHECK (is_privileged());
CREATE POLICY tickets_update_privileged ON tickets FOR UPDATE
  USING (is_privileged()) WITH CHECK (is_privileged());

-- ---- GRANTs ------------------------------------------------------------------
GRANT SELECT, INSERT, UPDATE ON ticket_types TO app_user, admin_dashboard;
GRANT SELECT, INSERT, UPDATE ON orders TO app_user, admin_dashboard;
GRANT SELECT, UPDATE ON orders TO worker_role;
GRANT SELECT, INSERT ON order_items TO app_user, admin_dashboard, worker_role;
GRANT SELECT, INSERT, UPDATE ON tickets TO app_user, admin_dashboard, worker_role;
GRANT EXECUTE ON FUNCTION reserve_ticket_inventory(uuid, integer) TO app_user, worker_role;
GRANT EXECUTE ON FUNCTION release_ticket_inventory(uuid, integer) TO app_user, worker_role;
GRANT EXECUTE ON FUNCTION check_in_ticket(text, uuid) TO app_user, admin_dashboard, worker_role;
GRANT EXECUTE ON FUNCTION enforce_order_item_currency() TO PUBLIC;

-- +goose Down
DROP TABLE IF EXISTS tickets;
DROP TABLE IF EXISTS order_items;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS ticket_types;
DROP FUNCTION IF EXISTS check_in_ticket(text, uuid);
DROP FUNCTION IF EXISTS release_ticket_inventory(uuid, integer);
DROP FUNCTION IF EXISTS reserve_ticket_inventory(uuid, integer);
DROP FUNCTION IF EXISTS enforce_order_item_currency();
DROP TYPE IF EXISTS ticket_status;
DROP TYPE IF EXISTS order_status;