package repository

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository-level sentinels for order states that need distinct API codes.
var (
	ErrTicketSoldOut       = errors.New("ticket_type_sold_out")
	ErrTicketSalesClosed   = errors.New("ticket_type_sales_closed")
	ErrOrderItemInvalid    = errors.New("invalid_order_items")
	ErrOrderNotCancellable = errors.New("order_not_cancellable")
	ErrOrderAlreadyPaid    = errors.New("order_already_paid")
)

// OrderItemInput is one requested line of an order under construction.
type OrderItemInput struct {
	TicketTypeID string
	Quantity     int
}

// OrderRepository manages orders, their items, issued tickets, and the
// atomic reserve/release/check-in SQL helpers.
type OrderRepository struct {
	pool *pgxpool.Pool
}

func NewOrderRepository(pool *pgxpool.Pool) *OrderRepository {
	return &OrderRepository{pool: pool}
}

func (r *OrderRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

// newUUID is an RFC4122 v4 UUID from crypto/rand (no external dependency).
// Used to pre-generate ticket ids so the QR code_hash (which embeds the id)
// can be computed before INSERT.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate uuid: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

const orderColumns = `id, user_id, event_id, status, currency, subtotal_minor, total_minor,
	payment_provider, provider_ref, created_at, updated_at, paid_at, cancelled_at`

func scanOrder(row pgx.Row) (*domain.Order, error) {
	var o domain.Order
	err := row.Scan(&o.ID, &o.UserID, &o.EventID, &o.Status, &o.Currency, &o.SubtotalMinor, &o.TotalMinor,
		&o.PaymentProvider, &o.ProviderRef, &o.CreatedAt, &o.UpdatedAt, &o.PaidAt, &o.CancelledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan order: %w", err)
	}
	return &o, nil
}

const orderItemColumns = `id, order_id, ticket_type_id,
	COALESCE((SELECT tt.name FROM ticket_types tt WHERE tt.id = order_items.ticket_type_id), '') AS ticket_type,
	quantity, currency, unit_price, subtotal, created_at`

func scanOrderItem(row pgx.Row) (*domain.OrderItem, error) {
	var it domain.OrderItem
	err := row.Scan(&it.ID, &it.OrderID, &it.TicketTypeID, &it.TicketType, &it.Quantity, &it.Currency,
		&it.UnitPrice, &it.Subtotal, &it.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan order item: %w", err)
	}
	return &it, nil
}

func (r *OrderRepository) selectOrderItems(ctx context.Context, q database.Querier, orderID string) ([]*domain.OrderItem, error) {
	rows, err := q.Query(ctx, `
		SELECT oi.id, oi.order_id, oi.ticket_type_id, COALESCE(tt.name, ''),
		       oi.quantity, oi.currency, oi.unit_price, oi.subtotal, oi.created_at
		FROM order_items oi
		JOIN ticket_types tt ON tt.id = oi.ticket_type_id
		WHERE oi.order_id = $1
		ORDER BY oi.created_at`, orderID)
	if err != nil {
		return nil, fmt.Errorf("select order items: %w", err)
	}
	defer rows.Close()

	var out []*domain.OrderItem
	for rows.Next() {
		it, err := scanOrderItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order items: %w", err)
	}
	return out, nil
}

// CreateOrder atomically reserves inventory for every line and inserts the
// pending order + items in one transaction (the request tx when present).
// Any unavailable line (sold out / sales closed / mismatched currency) aborts
// the whole order; the reservation is rolled back with the tx.
func (r *OrderRepository) CreateOrder(ctx context.Context, userID, eventID string, inputs []OrderItemInput) (*domain.Order, error) {
	tx, owned, err := database.Tx(ctx, r.pool)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback(ctx) }()
	}

	var currency string
	subtotal := int64(0)
	var items []*domain.OrderItem

	for _, in := range inputs {
		var tt domain.TicketType
		err := tx.QueryRow(ctx, `
			SELECT id, event_id, name, price_minor, currency, sales_start, sales_end, is_active
			FROM ticket_types
			WHERE id = $1 AND event_id = $2`, in.TicketTypeID, eventID).
			Scan(&tt.ID, &tt.EventID, &tt.Name, &tt.PriceMinor, &tt.Currency, &tt.SalesStart, &tt.SalesEnd, &tt.IsActive)
		if err == pgx.ErrNoRows {
			return nil, ErrOrderItemInvalid
		}
		if err != nil {
			return nil, fmt.Errorf("load ticket type: %w", err)
		}
		if !tt.IsActive {
			return nil, ErrTicketSalesClosed
		}
		now := time.Now()
		if tt.SalesStart != nil && now.Before(*tt.SalesStart) {
			return nil, ErrTicketSalesClosed
		}
		if tt.SalesEnd != nil && now.After(*tt.SalesEnd) {
			return nil, ErrTicketSalesClosed
		}
		if currency == "" {
			currency = tt.Currency
		} else if currency != tt.Currency {
			return nil, ErrOrderItemInvalid
		}

		var reserved bool
		if err := tx.QueryRow(ctx, `SELECT reserve_ticket_inventory($1, $2)`, tt.ID, in.Quantity).Scan(&reserved); err != nil {
			return nil, fmt.Errorf("reserve inventory: %w", err)
		}
		if !reserved {
			return nil, ErrTicketSoldOut
		}

		lineSubtotal := tt.PriceMinor * int64(in.Quantity)
		subtotal += lineSubtotal
		items = append(items, &domain.OrderItem{
			TicketTypeID: tt.ID,
			TicketType:   tt.Name,
			Currency:     tt.Currency,
			Quantity:     in.Quantity,
			UnitPrice:    tt.PriceMinor,
			Subtotal:     lineSubtotal,
		})
	}

	order, err := scanOrder(tx.QueryRow(ctx, `
		INSERT INTO orders (user_id, event_id, currency, subtotal_minor, total_minor)
		VALUES ($1, $2, $3, $4, $4)
		RETURNING `+orderColumns,
		userID, eventID, currency, subtotal))
	if err != nil {
		return nil, fmt.Errorf("insert order: %w", err)
	}

	for i := range items {
		it, err := scanOrderItem(tx.QueryRow(ctx, `
			INSERT INTO order_items (order_id, ticket_type_id, quantity, currency, unit_price, subtotal)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING `+orderItemColumns,
			order.ID, items[i].TicketTypeID, items[i].Quantity, items[i].Currency, items[i].UnitPrice, items[i].Subtotal))
		if err != nil {
			return nil, fmt.Errorf("insert order item: %w", err)
		}
		items[i] = it
	}
	order.Items = items

	if owned {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit order: %w", err)
		}
	}
	return order, nil
}

// GetOrderByID returns an order with its items. RLS scopes visibility to the
// buyer and the event's organizer (matching the orders_select policy).
func (r *OrderRepository) GetOrderByID(ctx context.Context, orderID string) (*domain.Order, error) {
	order, err := scanOrder(r.q(ctx).QueryRow(ctx, `
		SELECT `+orderColumns+` FROM orders WHERE id = $1`, orderID))
	if err != nil {
		return nil, err
	}
	items, err := r.selectOrderItems(ctx, r.q(ctx), orderID)
	if err != nil {
		return nil, err
	}
	order.Items = items
	return order, nil
}

// ListByUser returns the caller's orders, newest first.
func (r *OrderRepository) ListByUser(ctx context.Context, userID string, limit, offset int) ([]*domain.Order, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+orderColumns+`
		FROM orders WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()

	var out []*domain.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate orders: %w", err)
	}
	return out, nil
}

func (r *OrderRepository) CountByUser(ctx context.Context, userID string) (int, error) {
	var n int
	err := r.q(ctx).QueryRow(ctx, `SELECT count(*) FROM orders WHERE user_id = $1`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count orders: %w", err)
	}
	return n, nil
}

// Cancel flips the buyer's own pending order to cancelled and releases every
// reserved tier back to inventory, atomically. Non-pending or foreign orders
// are rejected. Pending orders can never hold issued tickets, so there is
// nothing else to unwind.
func (r *OrderRepository) Cancel(ctx context.Context, userID, orderID string) error {
	tx, owned, err := database.Tx(ctx, r.pool)
	if err != nil {
		return err
	}
	if owned {
		defer func() { _ = tx.Rollback(ctx) }()
	}

	var status string
	err = tx.QueryRow(ctx, `
		UPDATE orders SET status = 'cancelled', cancelled_at = now(), updated_at = now()
		WHERE id = $1 AND user_id = $2 AND status = 'pending'
		RETURNING status::text`, orderID, userID).Scan(&status)
	if err == pgx.ErrNoRows {
		var owner string
		serr := tx.QueryRow(ctx, `SELECT user_id FROM orders WHERE id = $1`, orderID).Scan(&owner)
		if serr != nil {
			return shared.ErrNotFound
		}
		return ErrOrderNotCancellable
	}
	if err != nil {
		return fmt.Errorf("cancel order: %w", err)
	}

	items, err := r.selectOrderItems(ctx, tx, orderID)
	if err != nil {
		return err
	}
	for _, it := range items {
		if _, err := tx.Exec(ctx, `SELECT release_ticket_inventory($1, $2)`, it.TicketTypeID, it.Quantity); err != nil {
			return fmt.Errorf("release inventory: %w", err)
		}
	}

	if owned {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit cancel: %w", err)
		}
	}
	return nil
}

// ConfirmPaid is the payment-success transition (Phase 15 webhook calls it;
// tests exercise it today). It flips a pending order to paid and issues one
// QR ticket per unit on every line, in one transaction. MUST run in a
// privileged ('service') RLS context: issuing tickets requires bypassing RLS
// (tickets writes are privileged-only by design).
func (r *OrderRepository) ConfirmPaid(ctx context.Context, orderID, provider, providerRef string) (*domain.Order, []*domain.Ticket, error) {
	tx, owned, err := database.Tx(ctx, r.pool)
	if err != nil {
		return nil, nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback(ctx) }()
	}

	order, err := scanOrder(tx.QueryRow(ctx, `
		UPDATE orders SET status = 'paid', paid_at = now(),
			payment_provider = $2, provider_ref = $3, updated_at = now()
		WHERE id = $1 AND status = 'pending'
		RETURNING `+orderColumns, orderID, provider, providerRef))
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			var status string
			if serr := tx.QueryRow(ctx, `SELECT status::text FROM orders WHERE id = $1`, orderID).Scan(&status); serr != nil {
				return nil, nil, shared.ErrNotFound
			}
			return nil, nil, ErrOrderAlreadyPaid
		}
		return nil, nil, fmt.Errorf("confirm paid: %w", err)
	}

	items, err := r.selectOrderItems(ctx, tx, orderID)
	if err != nil {
		return nil, nil, err
	}
	order.Items = items

	var tickets []*domain.Ticket
	for _, it := range items {
		for i := 0; i < it.Quantity; i++ {
			id, err := newUUID()
			if err != nil {
				return nil, nil, err
			}
			issuedAt := time.Now()
			codeHash := domain.TicketPayloadCodeHash(id, order.EventID, issuedAt)
			tk, err := scanTicket(tx.QueryRow(ctx, `
				INSERT INTO tickets (id, order_item_id, user_id, event_id, ticket_type_id, code_hash, issued_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				RETURNING `+ticketColumns,
				id, it.ID, order.UserID, order.EventID, it.TicketTypeID, codeHash, issuedAt))
			if err != nil {
				return nil, nil, fmt.Errorf("issue ticket: %w", err)
			}
			tickets = append(tickets, tk)
		}
	}

	if owned {
		if err := tx.Commit(ctx); err != nil {
			return nil, nil, fmt.Errorf("commit confirm: %w", err)
		}
	}
	return order, tickets, nil
}

// FailPayment flips a pending order to failed and releases every reserved tier
// back to inventory (a failed payment holds no stock). Runs in a privileged
// ('service') RLS context via the payment webhook. Idempotent: an order that is
// already failed/cancelled is a no-op; a paid order (the webhook lost a race or
// is duplicated) returns ErrOrderAlreadyPaid and its inventory is NOT released.
func (r *OrderRepository) FailPayment(ctx context.Context, orderID, provider, providerRef string) error {
	tx, owned, err := database.Tx(ctx, r.pool)
	if err != nil {
		return err
	}
	if owned {
		defer func() { _ = tx.Rollback(ctx) }()
	}

	tag, err := tx.Exec(ctx, `
		UPDATE orders SET status = 'failed',
			payment_provider = $2, provider_ref = $3, updated_at = now()
		WHERE id = $1 AND status = 'pending'`, orderID, provider, providerRef)
	if err != nil {
		return fmt.Errorf("fail payment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var status string
		if serr := tx.QueryRow(ctx, `SELECT status::text FROM orders WHERE id = $1`, orderID).Scan(&status); serr != nil {
			return shared.ErrNotFound
		}
		if status == "paid" {
			return ErrOrderAlreadyPaid
		}
		return nil // already failed/cancelled — nothing to release
	}

	items, err := r.selectOrderItems(ctx, tx, orderID)
	if err != nil {
		return err
	}
	for _, it := range items {
		if _, err := tx.Exec(ctx, `SELECT release_ticket_inventory($1, $2)`, it.TicketTypeID, it.Quantity); err != nil {
			return fmt.Errorf("release inventory: %w", err)
		}
	}

	if owned {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit fail payment: %w", err)
		}
	}
	return nil
}

// ListTicketsByUser returns the caller's active (issued) tickets.
func (r *OrderRepository) ListTicketsByUser(ctx context.Context, userID string) ([]*domain.Ticket, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+ticketColumns+`
		FROM tickets WHERE user_id = $1 AND status = 'issued'
		ORDER BY issued_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list tickets: %w", err)
	}
	defer rows.Close()

	var out []*domain.Ticket
	for rows.Next() {
		tk, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tk)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tickets: %w", err)
	}
	return out, nil
}

// GetTicketByID returns one ticket with its tier/event metadata. RLS scopes it
// to the holder and the event's organizer (tickets_select policy).
func (r *OrderRepository) GetTicketByID(ctx context.Context, ticketID string) (*domain.TicketWithMeta, error) {
	var t domain.TicketWithMeta
	err := r.q(ctx).QueryRow(ctx, `
		SELECT t.id, t.order_item_id, t.user_id, t.event_id, t.ticket_type_id, t.code_hash,
		       t.status, t.issued_at, t.used_at, t.created_at,
		       COALESCE(tt.name, '') AS ticket_type_name,
		       COALESCE(e.title, '') AS event_title
		FROM tickets t
		JOIN ticket_types tt ON tt.id = t.ticket_type_id
		JOIN events e ON e.id = t.event_id
		WHERE t.id = $1`, ticketID).
		Scan(&t.ID, &t.OrderItemID, &t.UserID, &t.EventID, &t.TicketTypeID, &t.CodeHash,
			&t.Status, &t.IssuedAt, &t.UsedAt, &t.CreatedAt, &t.TicketTypeName, &t.EventTitle)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan ticket meta: %w", err)
	}
	return &t, nil
}

// CheckIn runs the server-authoritative SQL check-in helper and returns its
// machine status: ok | already_used | not_found | forbidden.
func (r *OrderRepository) CheckIn(ctx context.Context, codeHash, eventID string) (string, error) {
	var status string
	err := r.q(ctx).QueryRow(ctx, `SELECT check_in_ticket($1, $2)`, codeHash, eventID).Scan(&status)
	if err != nil {
		return "", fmt.Errorf("check-in: %w", err)
	}
	return status, nil
}
