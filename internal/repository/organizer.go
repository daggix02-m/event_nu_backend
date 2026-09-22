package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OrganizerRepository struct {
	pool *pgxpool.Pool
}

func NewOrganizerRepository(pool *pgxpool.Pool) *OrganizerRepository {
	return &OrganizerRepository{pool: pool}
}

func (r *OrganizerRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

// CreateApplication records an organizer application.
func (r *OrganizerRepository) CreateApplication(ctx context.Context, a *domain.OrganizerApplication) error {
	data, err := json.Marshal(a.SupportingData)
	if err != nil {
		return fmt.Errorf("marshal supporting_data: %w", err)
	}
	err = r.q(ctx).QueryRow(ctx, `
		INSERT INTO organizer_applications (user_id, requested_name, requested_slug, bio, supporting_data)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at`,
		a.UserID, a.RequestedName, a.RequestedSlug, a.Bio, data).Scan(&a.ID, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert organizer_application: %w", err)
	}
	return nil
}

func scanApplication(row pgx.Row) (*domain.OrganizerApplication, error) {
	var a domain.OrganizerApplication
	var data []byte
	err := row.Scan(&a.ID, &a.UserID, &a.RequestedName, &a.RequestedSlug, &a.Bio, &data,
		&a.Status, &a.ReviewedBy, &a.ReviewedAt, &a.ReviewNotes, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan organizer_application: %w", err)
	}
	if err := json.Unmarshal(data, &a.SupportingData); err != nil {
		return nil, fmt.Errorf("unmarshal supporting_data: %w", err)
	}
	return &a, nil
}

const applicationColumns = "id, user_id, requested_name, requested_slug, bio, supporting_data, status, reviewed_by, reviewed_at, COALESCE(review_notes, ''), created_at, updated_at"

func (r *OrganizerRepository) GetApplicationByUser(ctx context.Context, userID string) (*domain.OrganizerApplication, error) {
	return scanApplication(r.q(ctx).QueryRow(ctx, `
		SELECT `+applicationColumns+`
		FROM organizer_applications
		WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1`, userID))
}

func (r *OrganizerRepository) GetApplicationByID(ctx context.Context, id string) (*domain.OrganizerApplication, error) {
	return scanApplication(r.q(ctx).QueryRow(ctx, `
		SELECT `+applicationColumns+`
		FROM organizer_applications WHERE id = $1`, id))
}

// ApproveApplication atomically transitions a pending application to approved.
// The `WHERE status = 'pending'` guard means competing reviewers both run the
// UPDATE, but Postgres re-evaluates the predicate after the first commits, so
// exactly one wins; the loser matches zero rows and gets a 409 conflict.
func (r *OrganizerRepository) ApproveApplication(ctx context.Context, id, adminUserID, notes string) (*domain.OrganizerApplication, error) {
	app, err := scanApplication(r.q(ctx).QueryRow(ctx, `
		UPDATE organizer_applications
		SET status = 'approved', reviewed_by = $2, reviewed_at = now(), review_notes = $3, updated_at = now()
		WHERE id = $1 AND status = 'pending'
		RETURNING `+applicationColumns,
		id, adminUserID, notes))
	if err == nil {
		return app, nil
	}
	if errors.Is(err, shared.ErrNotFound) {
		return nil, fmt.Errorf("%w: application_not_pending", shared.ErrConflict)
	}
	return nil, fmt.Errorf("approve organizer_application: %w", err)
}

// RejectApplication atomically transitions a pending application to rejected.
func (r *OrganizerRepository) RejectApplication(ctx context.Context, id, adminUserID, notes string) error {
	tag, err := r.q(ctx).Exec(ctx, `
		UPDATE organizer_applications
		SET status = 'rejected', reviewed_by = $2, reviewed_at = now(), review_notes = $3, updated_at = now()
		WHERE id = $1 AND status = 'pending'`,
		id, adminUserID, notes)
	if err != nil {
		return fmt.Errorf("reject organizer_application: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: application_not_pending", shared.ErrConflict)
	}
	return nil
}

func scanOrganizer(row pgx.Row) (*domain.Organizer, error) {
	var o domain.Organizer
	err := row.Scan(&o.ID, &o.OwnerUserID, &o.Slug, &o.Name, &o.Bio, &o.Status, &o.CreatedAt, &o.UpdatedAt, &o.DeletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan organizer: %w", err)
	}
	return &o, nil
}

const organizerColumns = "id, owner_user_id, slug, name, bio, status, created_at, updated_at, deleted_at"

func (r *OrganizerRepository) CreateOrganizer(ctx context.Context, o *domain.Organizer) (*domain.Organizer, error) {
	return scanOrganizer(r.q(ctx).QueryRow(ctx, `
		INSERT INTO organizers (owner_user_id, slug, name, bio)
		VALUES ($1, $2, $3, $4)
		RETURNING `+organizerColumns,
		o.OwnerUserID, o.Slug, o.Name, o.Bio))
}

func (r *OrganizerRepository) GetOrganizerByID(ctx context.Context, id string) (*domain.Organizer, error) {
	return scanOrganizer(r.q(ctx).QueryRow(ctx, `
		SELECT `+organizerColumns+`
		FROM organizers WHERE id = $1 AND deleted_at IS NULL`, id))
}

func (r *OrganizerRepository) GetOrganizerByOwner(ctx context.Context, userID string) (*domain.Organizer, error) {
	return scanOrganizer(r.q(ctx).QueryRow(ctx, `
		SELECT `+organizerColumns+`
		FROM organizers WHERE owner_user_id = $1 AND deleted_at IS NULL LIMIT 1`, userID))
}

const applicationQueueColumns = "oa.id, oa.user_id, oa.requested_name, oa.requested_slug, oa.bio, oa.supporting_data, oa.status, oa.reviewed_by, oa.reviewed_at, COALESCE(oa.review_notes, ''), oa.created_at, oa.updated_at, u.username, u.email"

func scanApplicationQueueItem(row pgx.Row) (*domain.OrganizerApplication, error) {
	var a domain.OrganizerApplication
	var data []byte
	err := row.Scan(&a.ID, &a.UserID, &a.RequestedName, &a.RequestedSlug, &a.Bio, &data,
		&a.Status, &a.ReviewedBy, &a.ReviewedAt, &a.ReviewNotes, &a.CreatedAt, &a.UpdatedAt,
		&a.ApplicantUsername, &a.ApplicantEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan organizer_application queue item: %w", err)
	}
	if err := json.Unmarshal(data, &a.SupportingData); err != nil {
		return nil, fmt.Errorf("unmarshal supporting_data: %w", err)
	}
	return &a, nil
}

func (r *OrganizerRepository) ListApplications(ctx context.Context, status string, limit, offset int) ([]*domain.OrganizerApplication, error) {
	where, args := "", []any{}
	if status != "" {
		where = "WHERE oa.status = $1"
		args = append(args, status)
	}
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+applicationQueueColumns+`
		FROM organizer_applications oa
		LEFT JOIN users u ON u.id = oa.user_id
		`+where+`
		ORDER BY (oa.status = 'pending') DESC, oa.created_at DESC, oa.id
		LIMIT $`+numArg(len(args)+1)+` OFFSET $`+numArg(len(args)+2),
		append(args, limit, offset)...)
	if err != nil {
		return nil, fmt.Errorf("list organizer_applications: %w", err)
	}
	defer rows.Close()

	var apps []*domain.OrganizerApplication
	for rows.Next() {
		app, err := scanApplicationQueueItem(rows)
		if err != nil {
			return nil, err
		}
		apps = append(apps, app)
	}
	return apps, rows.Err()
}

func (r *OrganizerRepository) CountApplications(ctx context.Context, status string) (int, error) {
	where, args := "", []any{}
	if status != "" {
		where = "WHERE status = $1"
		args = append(args, status)
	}
	var total int
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) FROM organizer_applications
		`+where, args...).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("count organizer_applications: %w", err)
	}
	return total, nil
}
