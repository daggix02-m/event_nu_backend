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

// ReportRepository records moderation reports and applies the under_review
// moderation transition (via a SECURITY DEFINER helper so reporters can flag
// content they do not own).
type ReportRepository struct {
	pool *pgxpool.Pool
}

func NewReportRepository(pool *pgxpool.Pool) *ReportRepository {
	return &ReportRepository{pool: pool}
}

func (r *ReportRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const reportColumns = "id, reporter_user_id, entity_type, entity_id, reason_code, COALESCE(description, ''), status, resolution, resolved_at, created_at, updated_at"

func scanReport(row pgx.Row) (*domain.Report, error) {
	var r domain.Report
	err := row.Scan(&r.ID, &r.ReporterUserID, &r.EntityType, &r.EntityID, &r.ReasonCode, &r.Description,
		&r.Status, &r.Resolution, &r.ResolvedAt, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan report: %w", err)
	}
	return &r, nil
}

// ListAll returns all reports newest-first (admin RLS role sees every row).
func (r *ReportRepository) ListAll(ctx context.Context, limit, offset int) ([]*domain.Report, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+reportColumns+`
		FROM reports
		ORDER BY created_at DESC, id
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list reports: %w", err)
	}
	defer rows.Close()

	var out []*domain.Report
	for rows.Next() {
		rep, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rep)
	}
	return out, rows.Err()
}

func (r *ReportRepository) CountAll(ctx context.Context) (int, error) {
	var total int
	err := r.q(ctx).QueryRow(ctx, `SELECT count(*) FROM reports`).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("count reports: %w", err)
	}
	return total, nil
}

// Resolve closes a report as dismissed/resolved. Returns ErrNotFound when the
// report does not exist or was already resolved.
func (r *ReportRepository) Resolve(ctx context.Context, id, resolution string) (*domain.Report, error) {
	return scanReport(r.q(ctx).QueryRow(ctx, `
		UPDATE reports SET
			status = 'resolved', resolution = NULLIF($2, ''), updated_at = now()
		WHERE id = $1 AND status <> 'resolved'
		RETURNING `+reportColumns, id, resolution))
}

func (r *ReportRepository) Create(ctx context.Context, rep *domain.Report) (*domain.Report, error) {
	tx := r.q(ctx)
	err := tx.QueryRow(ctx, `
		INSERT INTO reports (reporter_user_id, entity_type, entity_id, reason_code, description)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''))
		RETURNING id, status, created_at`,
		rep.ReporterUserID, rep.EntityType, rep.EntityID, rep.ReasonCode, rep.Description).
		Scan(&rep.ID, &rep.Status, &rep.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert report: %w", err)
	}
	// Transition the target to 'under_review' only — never auto-hide.
	if _, err := tx.Exec(ctx, `SELECT mark_target_reported($1, $2)`, rep.EntityType, rep.EntityID); err != nil {
		return nil, fmt.Errorf("mark target reported: %w", err)
	}
	return rep, nil
}
