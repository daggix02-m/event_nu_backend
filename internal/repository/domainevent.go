package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/domain"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DomainEventRepository is the SQL adapter for the transactional outbox.
// Write executes on the CALLER's transaction when the context carries one —
// that is the guarantee: the fact row lives or dies with the state change.
// ClaimBatch/MarkSent run on the worker's service-role connection.
type DomainEventRepository struct {
	pool *pgxpool.Pool
}

func NewDomainEventRepository(pool *pgxpool.Pool) *DomainEventRepository {
	return &DomainEventRepository{pool: pool}
}

func (r *DomainEventRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

func (r *DomainEventRepository) Write(ctx context.Context, ev domain.DomainEvent, ownerUserID string) error {
	payload, err := json.Marshal(json.RawMessage(ev.Payload))
	if err != nil {
		return fmt.Errorf("marshal domain event payload: %w", err)
	}
	_, err = r.q(ctx).Exec(ctx, `
		INSERT INTO domain_events (name, key, payload, owner_user_id, occurred_at)
		VALUES ($1, $2, $3, $4, $5)`,
		ev.Name, ev.Key, payload, nullIfEmpty(ownerUserID), ev.OccurredAt)
	if err != nil {
		return fmt.Errorf("insert domain event: %w", err)
	}
	return nil
}

// ClaimBatch claims due rows, mirroring email_outbox's lease: next_retry_at
// is pushed forward so a crashed worker's stale lease re-queues the row
// instead of orphaning it. Attempts increments on every claim, giving
// observability into delivery health.
func (r *DomainEventRepository) ClaimBatch(ctx context.Context, batchSize int, lease time.Duration) ([]domain.DomainEventDelivery, error) {
	rows, err := r.q(ctx).Query(ctx, `
		UPDATE domain_events
		SET next_retry_at = now() + $2, attempts = attempts + 1
		WHERE id IN (
			SELECT id FROM domain_events
			WHERE status = 'pending' AND next_retry_at <= now()
			ORDER BY produced_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, name, key, payload, occurred_at, attempts`,
		batchSize, lease.String())
	if err != nil {
		return nil, fmt.Errorf("claim domain event batch: %w", err)
	}
	defer rows.Close()

	var items []domain.DomainEventDelivery
	for rows.Next() {
		var d domain.DomainEventDelivery
		var ev domain.DomainEvent
		var raw []byte
		if err := rows.Scan(&d.ID, &ev.Name, &ev.Key, &raw, &ev.OccurredAt, &d.Attempts); err != nil {
			return nil, fmt.Errorf("scan domain event batch: %w", err)
		}
		// jsonb comes back as its JSON bytes; no re-marshal needed.
		ev.Payload = raw
		d.Event = ev
		items = append(items, d)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("iterate domain event batch: %w", rows.Err())
	}
	return items, nil
}

// MarkSent flips a delivered row. A plain (transaction-less) update is
// enough: claiming already leased the row, so exactly one worker is here.
func (r *DomainEventRepository) MarkSent(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE domain_events
		SET status = 'sent', sent_at = now(), next_retry_at = now()
		WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark domain event sent: %w", err)
	}
	return nil
}

// Compile-time proofs for both seams.
var _ domain.DomainEventWriter = (*DomainEventRepository)(nil)
var _ domain.DomainEventOutbox = (*DomainEventRepository)(nil)
