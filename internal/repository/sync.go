package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SyncRepository serves the cursor-based offline delta endpoint (Phase 16).
//
// Every ChangedSince method takes a `limit` and internally reads limit+1 rows,
// so the caller can distinguish "page full" (more rows exist) from "domain
// exhausted" without a second count query. Rows are emitted in (updated_at, id)
// order, the same order the client resumes from with the returned cursor. The
// cursor is inclusive (updated_at >= since): re-polling with the previous
// response's cursor re-emits the boundary row, which is how same-timestamp
// batches make guaranteed forward progress.
type SyncRepository struct {
	pool *pgxpool.Pool
}

func NewSyncRepository(pool *pgxpool.Pool) *SyncRepository {
	return &SyncRepository{pool: pool}
}

func (r *SyncRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

// SyncPage is one domain page: up to `limit` rows to emit, the watermark the
// caller's next cursor will be advanced to (the max updated_at/change time over
// every row READ this page, including the limit+1th sentinel that only proves
// more rows exist), and HasMore. Using the full read horizon for the watermark
// is what lets a boundary row be re-emitted without stalling pagination.
type SyncPage[T any] struct {
	Rows      []T
	Watermark time.Time
	HasMore   bool
}

// pageOf trims a limit+1 row set to `limit` and derives the read-horizon
// watermark and has-more flag.
func pageOf[T any](rows []T, since time.Time, limit int, stamp func(T) time.Time) SyncPage[T] {
	wm := since
	for _, r := range rows {
		if t := stamp(r); t.After(wm) {
			wm = t
		}
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	return SyncPage[T]{Rows: rows, Watermark: wm, HasMore: hasMore}
}

// DeletedChange is a soft-deleted row id plus the timestamp at which the row
// changed (the delete's updated_at bump), so the caller can fold it into its
// per-domain watermark.
type DeletedChange struct {
	ID        string
	ChangedAt time.Time
}

func (r *SyncRepository) EventsChangedSince(ctx context.Context, since time.Time, limit int) (SyncPage[*domain.Event], error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+eventColumns+`
		FROM events
		WHERE updated_at >= $1
		  AND status = 'published' AND moderation_status <> 'blocked' AND deleted_at IS NULL
		ORDER BY updated_at, id
		LIMIT $2`, since, limit+1)
	if err != nil {
		return SyncPage[*domain.Event]{}, fmt.Errorf("sync events: %w", err)
	}
	defer rows.Close()

	var out []*domain.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return SyncPage[*domain.Event]{}, fmt.Errorf("scan sync event: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return SyncPage[*domain.Event]{}, fmt.Errorf("iterate sync events: %w", err)
	}
	return pageOf(out, since, limit, func(e *domain.Event) time.Time { return e.UpdatedAt }), nil
}

func (r *SyncRepository) VenuesChangedSince(ctx context.Context, since time.Time, limit int) (SyncPage[*domain.Venue], error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+venueColumns+`
		FROM venues
		WHERE updated_at >= $1 AND status = 'active' AND deleted_at IS NULL
		ORDER BY updated_at, id
		LIMIT $2`, since, limit+1)
	if err != nil {
		return SyncPage[*domain.Venue]{}, fmt.Errorf("sync venues: %w", err)
	}
	defer rows.Close()

	var out []*domain.Venue
	for rows.Next() {
		v, err := scanVenue(rows)
		if err != nil {
			return SyncPage[*domain.Venue]{}, fmt.Errorf("scan sync venue: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return SyncPage[*domain.Venue]{}, fmt.Errorf("iterate sync venues: %w", err)
	}
	return pageOf(out, since, limit, func(v *domain.Venue) time.Time { return v.UpdatedAt }), nil
}

func (r *SyncRepository) OrganizersChangedSince(ctx context.Context, since time.Time, limit int) (SyncPage[*domain.Organizer], error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+organizerColumns+`
		FROM organizers
		WHERE updated_at >= $1 AND status = 'active' AND deleted_at IS NULL
		ORDER BY updated_at, id
		LIMIT $2`, since, limit+1)
	if err != nil {
		return SyncPage[*domain.Organizer]{}, fmt.Errorf("sync organizers: %w", err)
	}
	defer rows.Close()

	var out []*domain.Organizer
	for rows.Next() {
		o, err := scanOrganizer(rows)
		if err != nil {
			return SyncPage[*domain.Organizer]{}, fmt.Errorf("scan sync organizer: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return SyncPage[*domain.Organizer]{}, fmt.Errorf("iterate sync organizers: %w", err)
	}
	return pageOf(out, since, limit, func(o *domain.Organizer) time.Time { return o.UpdatedAt }), nil
}

func (r *SyncRepository) CategoriesChangedSince(ctx context.Context, since time.Time, limit int) (SyncPage[*domain.Category], error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+categoryColumns+`
		FROM categories
		WHERE is_active AND updated_at >= $1
		ORDER BY updated_at, id
		LIMIT $2`, since, limit+1)
	if err != nil {
		return SyncPage[*domain.Category]{}, fmt.Errorf("sync categories: %w", err)
	}
	defer rows.Close()

	var out []*domain.Category
	for rows.Next() {
		var c domain.Category
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &c.SortOrder, &c.IsActive, &c.UpdatedAt); err != nil {
			return SyncPage[*domain.Category]{}, fmt.Errorf("scan sync category: %w", err)
		}
		out = append(out, &c)
	}
	if err := rows.Err(); err != nil {
		return SyncPage[*domain.Category]{}, fmt.Errorf("iterate sync categories: %w", err)
	}
	return pageOf(out, since, limit, func(c *domain.Category) time.Time { return c.UpdatedAt }), nil
}

func (r *SyncRepository) TicketTypesChangedSince(ctx context.Context, since time.Time, limit int) (SyncPage[*domain.TicketType], error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+ticketTypeColumns+`
		FROM ticket_types
		WHERE is_active AND updated_at >= $1
		ORDER BY updated_at, id
		LIMIT $2`, since, limit+1)
	if err != nil {
		return SyncPage[*domain.TicketType]{}, fmt.Errorf("sync ticket types: %w", err)
	}
	defer rows.Close()

	var out []*domain.TicketType
	for rows.Next() {
		tt, err := scanTicketType(rows)
		if err != nil {
			return SyncPage[*domain.TicketType]{}, fmt.Errorf("scan sync ticket type: %w", err)
		}
		out = append(out, tt)
	}
	if err := rows.Err(); err != nil {
		return SyncPage[*domain.TicketType]{}, fmt.Errorf("iterate sync ticket types: %w", err)
	}
	return pageOf(out, since, limit, func(tt *domain.TicketType) time.Time { return tt.UpdatedAt }), nil
}

func (r *SyncRepository) CommentsChangedSince(ctx context.Context, since time.Time, limit int) (SyncPage[*domain.Comment], error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+commentColumns+`
		FROM comments
		WHERE updated_at >= $1 AND deleted_at IS NULL AND moderation_status <> 'blocked'
		ORDER BY updated_at, id
		LIMIT $2`, since, limit+1)
	if err != nil {
		return SyncPage[*domain.Comment]{}, fmt.Errorf("sync comments: %w", err)
	}
	defer rows.Close()

	var out []*domain.Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return SyncPage[*domain.Comment]{}, fmt.Errorf("scan sync comment: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return SyncPage[*domain.Comment]{}, fmt.Errorf("iterate sync comments: %w", err)
	}
	return pageOf(out, since, limit, func(c *domain.Comment) time.Time { return c.UpdatedAt }), nil
}

func (r *SyncRepository) ReviewsChangedSince(ctx context.Context, since time.Time, limit int) (SyncPage[*domain.Review], error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+reviewColumns+`
		FROM reviews
		WHERE updated_at >= $1 AND status = 'published' AND deleted_at IS NULL
		ORDER BY updated_at, id
		LIMIT $2`, since, limit+1)
	if err != nil {
		return SyncPage[*domain.Review]{}, fmt.Errorf("sync reviews: %w", err)
	}
	defer rows.Close()

	var out []*domain.Review
	for rows.Next() {
		rev, err := scanReview(rows)
		if err != nil {
			return SyncPage[*domain.Review]{}, fmt.Errorf("scan sync review: %w", err)
		}
		out = append(out, rev)
	}
	if err := rows.Err(); err != nil {
		return SyncPage[*domain.Review]{}, fmt.Errorf("iterate sync reviews: %w", err)
	}
	return pageOf(out, since, limit, func(rev *domain.Review) time.Time { return rev.UpdatedAt }), nil
}

// DeletedIDsChangedSince returns soft-deleted row ids (and their change
// timestamps) for a delete-capable domain via the privileged
// sync_deleted_ids() helper. Limit semantics match the ChangedSince methods.
func (r *SyncRepository) DeletedIDsChangedSince(ctx context.Context, since time.Time, domainName string, limit int) (SyncPage[DeletedChange], error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT row_id, changed_at FROM sync_deleted_ids($1, $2, $3)`,
		since, domainName, limit+1)
	if err != nil {
		return SyncPage[DeletedChange]{}, fmt.Errorf("sync deleted ids: %w", err)
	}
	defer rows.Close()

	var out []DeletedChange
	for rows.Next() {
		var c DeletedChange
		if err := rows.Scan(&c.ID, &c.ChangedAt); err != nil {
			return SyncPage[DeletedChange]{}, fmt.Errorf("scan sync deleted id: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return SyncPage[DeletedChange]{}, fmt.Errorf("iterate sync deleted ids: %w", err)
	}
	return pageOf(out, since, limit, func(c DeletedChange) time.Time { return c.ChangedAt }), nil
}
