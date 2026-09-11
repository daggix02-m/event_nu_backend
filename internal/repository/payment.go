package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PaymentRepository manages the payments ledger. A pending row is opened by the
// card-init flow through the SECURITY DEFINER record_pending_payment() helper
// (a plain user cannot INSERT payments — RLS permits privileged roles only).
// Every later transition arrives from the provider webhook (privileged context)
// through UpsertFromWebhook, so the ledger stays provider-authoritative.
type PaymentRepository struct {
	pool *pgxpool.Pool
}

func NewPaymentRepository(pool *pgxpool.Pool) *PaymentRepository {
	return &PaymentRepository{pool: pool}
}

func (r *PaymentRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const paymentColumns = `id, order_id, provider, provider_payment_id, status,
	amount_minor, currency, created_at, updated_at, paid_at, failed_at`

func scanPayment(row pgx.Row) (*domain.Payment, error) {
	var p domain.Payment
	err := row.Scan(&p.ID, &p.OrderID, &p.Provider, &p.ProviderPaymentID, &p.Status,
		&p.AmountMinor, &p.Currency, &p.CreatedAt, &p.UpdatedAt, &p.PaidAt, &p.FailedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan payment: %w", err)
	}
	return &p, nil
}

// RecordPending opens the ledger row for a just-started checkout. Safe for the
// purchasing user (SECURITY DEFINER). If an identical row already exists (an
// Idempotency-Key retry raced its way past the response cache) the insert is a
// no-op rather than an error, keeping retries idempotent.
func (r *PaymentRepository) RecordPending(ctx context.Context, p *domain.Payment) error {
	err := r.q(ctx).QueryRow(ctx, `
		SELECT record_pending_payment($1, $2, $3, $4, $5)`,
		p.OrderID, p.Provider, p.ProviderPaymentID, p.AmountMinor, p.Currency).Scan(new(string))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil
		}
		return fmt.Errorf("record pending payment: %w", err)
	}
	return nil
}

// UpsertFromWebhook writes the provider's authoritative money state for a
// transaction. Insert-on-first-event, idempotent afterwards; paid/failed
// timestamps are only ever set once (COALESCE keeps the first value).
func (r *PaymentRepository) UpsertFromWebhook(ctx context.Context, p *domain.Payment, rawBody []byte) error {
	raw := "null"
	if len(rawBody) > 0 {
		raw = string(rawBody)
	}
	_, err := r.q(ctx).Exec(ctx, `
		INSERT INTO payments (order_id, provider, provider_payment_id, status, amount_minor, currency, raw_reference, paid_at, failed_at)
		VALUES ($1, $2, $3, $4::payment_status, $5, $6, $7::jsonb,
		        CASE WHEN $4::payment_status = 'paid' THEN now() END,
		        CASE WHEN $4::payment_status = 'failed' THEN now() END)
		ON CONFLICT (provider, provider_payment_id) DO UPDATE SET
			status = EXCLUDED.status,
			raw_reference = COALESCE(EXCLUDED.raw_reference, payments.raw_reference),
			paid_at = COALESCE(EXCLUDED.paid_at, payments.paid_at),
			failed_at = COALESCE(EXCLUDED.failed_at, payments.failed_at),
			updated_at = now()`,
		p.OrderID, p.Provider, p.ProviderPaymentID, p.Status, p.AmountMinor, p.Currency, raw)
	if err != nil {
		return fmt.Errorf("upsert payment: %w", err)
	}
	return nil
}

// GetByOrder returns the most recent ledger row for an order, or
// shared.ErrNotFound when none exists yet.
func (r *PaymentRepository) GetByOrder(ctx context.Context, orderID string) (*domain.Payment, error) {
	return scanPayment(r.q(ctx).QueryRow(ctx, `
		SELECT `+paymentColumns+`
		FROM payments
		WHERE order_id = $1
		ORDER BY updated_at DESC, created_at DESC
		LIMIT 1`, orderID))
}
