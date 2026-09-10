package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TicketTypeRepository manages event ticket tiers. quantity_sold is only ever
// moved by the reserve/release SQL helpers — this repository never writes it.
type TicketTypeRepository struct {
	pool *pgxpool.Pool
}

func NewTicketTypeRepository(pool *pgxpool.Pool) *TicketTypeRepository {
	return &TicketTypeRepository{pool: pool}
}

func (r *TicketTypeRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const ticketColumns = `id, order_item_id, user_id, event_id, ticket_type_id, code_hash, status, issued_at, used_at, created_at`

func scanTicket(row pgx.Row) (*domain.Ticket, error) {
	var t domain.Ticket
	err := row.Scan(&t.ID, &t.OrderItemID, &t.UserID, &t.EventID, &t.TicketTypeID, &t.CodeHash,
		&t.Status, &t.IssuedAt, &t.UsedAt, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan ticket: %w", err)
	}
	return &t, nil
}

const ticketTypeColumns = `id, event_id, name, description, price_minor, currency,
	quantity_total, quantity_sold, sales_start, sales_end, is_active, created_at, updated_at`

func scanTicketType(row pgx.Row) (*domain.TicketType, error) {
	var tt domain.TicketType
	err := row.Scan(&tt.ID, &tt.EventID, &tt.Name, &tt.Description, &tt.PriceMinor, &tt.Currency,
		&tt.QuantityTotal, &tt.QuantitySold, &tt.SalesStart, &tt.SalesEnd, &tt.IsActive,
		&tt.CreatedAt, &tt.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan ticket type: %w", err)
	}
	return &tt, nil
}

// Create inserts a new tier for an event the caller's organizer owns (RLS).
func (r *TicketTypeRepository) Create(ctx context.Context, tt *domain.TicketType) (*domain.TicketType, error) {
	return scanTicketType(r.q(ctx).QueryRow(ctx, `
		INSERT INTO ticket_types (event_id, name, description, price_minor, currency, quantity_total, sales_start, sales_end)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+ticketTypeColumns,
		tt.EventID, tt.Name, tt.Description, tt.PriceMinor, tt.Currency, tt.QuantityTotal, tt.SalesStart, tt.SalesEnd))
}

func (r *TicketTypeRepository) GetByID(ctx context.Context, id string) (*domain.TicketType, error) {
	return scanTicketType(r.q(ctx).QueryRow(ctx, `
		SELECT `+ticketTypeColumns+`
		FROM ticket_types WHERE id = $1`, id))
}

// Update edits the mutable fields of an organizer-owned tier. quantity_sold
// and event_id are intentionally excluded (inventory only moves via the SQL
// helpers; re-targeting a tier would break the purchase chain).
func (r *TicketTypeRepository) Update(ctx context.Context, tt *domain.TicketType) (*domain.TicketType, error) {
	return scanTicketType(r.q(ctx).QueryRow(ctx, `
		UPDATE ticket_types SET
			name = $2, description = $3, price_minor = $4, quantity_total = $5,
			sales_start = $6, sales_end = $7, is_active = $8, updated_at = now()
		WHERE id = $1
		RETURNING `+ticketTypeColumns,
		tt.ID, tt.Name, tt.Description, tt.PriceMinor, tt.QuantityTotal, tt.SalesStart, tt.SalesEnd, tt.IsActive))
}

// ListByEvent returns every tier of an event (organizer context; includes
// inactive and window-closed tiers). RLS scopes it to organizer-owned events.
func (r *TicketTypeRepository) ListByEvent(ctx context.Context, eventID string) ([]*domain.TicketType, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+ticketTypeColumns+`
		FROM ticket_types
		WHERE event_id = $1
		ORDER BY price_minor, created_at`, eventID)
	if err != nil {
		return nil, fmt.Errorf("list ticket types: %w", err)
	}
	defer rows.Close()

	var out []*domain.TicketType
	for rows.Next() {
		tt, err := scanTicketType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ticket types: %w", err)
	}
	return out, nil
}

// ListByEventPublic returns active tiers of an event (public read; RLS shows
// only is_active rows).
func (r *TicketTypeRepository) ListByEventPublic(ctx context.Context, eventID string) ([]*domain.TicketType, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+ticketTypeColumns+`
		FROM ticket_types
		WHERE event_id = $1 AND is_active
		ORDER BY price_minor, created_at`, eventID)
	if err != nil {
		return nil, fmt.Errorf("list public ticket types: %w", err)
	}
	defer rows.Close()

	var out []*domain.TicketType
	for rows.Next() {
		tt, err := scanTicketType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate public ticket types: %w", err)
	}
	return out, nil
}
