package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EventRepository struct {
	pool *pgxpool.Pool
}

func NewEventRepository(pool *pgxpool.Pool) *EventRepository {
	return &EventRepository{pool: pool}
}

func (r *EventRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const eventColumns = `id, organizer_id, venue_id, category_id, title, COALESCE(description, '') AS description, starts_at, ends_at,
	price_is_free, COALESCE(price_display, '') AS price_display, action_type, COALESCE(action_target, '') AS action_target, status, moderation_status, max_attendees,
	poster_media_id, teaser_media_id, created_at, updated_at, deleted_at`

// eventColumnsQualified is eventColumns aliased to `e` for queries that join
// venues (full-text and proximity search), where bare column names are
// ambiguous.
const eventColumnsQualified = `e.id, e.organizer_id, e.venue_id, e.category_id, e.title, COALESCE(e.description, '') AS description, e.starts_at, e.ends_at,
	e.price_is_free, COALESCE(e.price_display, '') AS price_display, e.action_type, COALESCE(e.action_target, '') AS action_target, e.status, e.moderation_status, e.max_attendees,
	e.poster_media_id, e.teaser_media_id, e.created_at, e.updated_at, e.deleted_at`

func scanEvent(row pgx.Row) (*domain.Event, error) {
	var e domain.Event
	err := row.Scan(&e.ID, &e.OrganizerID, &e.VenueID, &e.CategoryID, &e.Title, &e.Description,
		&e.StartsAt, &e.EndsAt, &e.PriceIsFree, &e.PriceDisplay, &e.ActionType, &e.ActionTarget,
		&e.Status, &e.ModerationStatus, &e.MaxAttendees, &e.PosterMediaID, &e.TeaserMediaID,
		&e.CreatedAt, &e.UpdatedAt, &e.DeletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan event: %w", err)
	}
	return &e, nil
}

func (r *EventRepository) Create(ctx context.Context, e *domain.Event) (*domain.Event, error) {
	return scanEvent(r.q(ctx).QueryRow(ctx, `
		INSERT INTO events (organizer_id, venue_id, category_id, title, description, starts_at, ends_at,
			price_is_free, price_display, action_type, action_target, max_attendees,
			poster_media_id, teaser_media_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING `+eventColumns,
		e.OrganizerID, e.VenueID, e.CategoryID, e.Title, e.Description, e.StartsAt, e.EndsAt,
		e.PriceIsFree, e.PriceDisplay, e.ActionType, e.ActionTarget, e.MaxAttendees,
		e.PosterMediaID, e.TeaserMediaID))
}

// GetByID returns any event by id (owner/privileged visibility via RLS).
func (r *EventRepository) GetByID(ctx context.Context, id string) (*domain.Event, error) {
	return scanEvent(r.q(ctx).QueryRow(ctx, `
		SELECT `+eventColumns+`
		FROM events WHERE id = $1 AND deleted_at IS NULL`, id))
}

// ListVisible returns published, non-blocked events matching filters, ordered
// by FTS relevance when a query is present, else by start time.
func (r *EventRepository) ListVisible(ctx context.Context, filters domain.EventFilters, limit, offset int) ([]*domain.Event, error) {
	where, args, hasQuery := eventSearchClause(filters)
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+eventColumnsQualified+`
		FROM events e
		LEFT JOIN venues v ON v.id = e.venue_id
		WHERE `+where+`
		ORDER BY `+eventSearchOrder(hasQuery)+` e.starts_at, e.id
		LIMIT $`+numArg(len(args)+1)+` OFFSET $`+numArg(len(args)+2),
		append(args, limit, offset)...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	var events []*domain.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// CountVisible returns the total number of published, non-blocked events
// matching filters.
func (r *EventRepository) CountVisible(ctx context.Context, filters domain.EventFilters) (int, error) {
	where, args, _ := eventSearchClause(filters)
	var total int
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) FROM events e
		LEFT JOIN venues v ON v.id = e.venue_id
		WHERE `+where,
		args...).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("count events: %w", err)
	}
	return total, nil
}

// SetModeration updates an event's moderation_status only (EventStatus is
// deliberately untouched — moderation and lifecycle stay orthogonal).
func (r *EventRepository) SetModeration(ctx context.Context, id, moderation string) error {
	tag, err := r.q(ctx).Exec(ctx, `
		UPDATE events SET moderation_status = $2, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL`, id, moderation)
	if err != nil {
		return fmt.Errorf("set event moderation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return shared.ErrNotFound
	}
	return nil
}

// adminEventClause builds the WHERE clause for the admin event list. Filters
// are validated at the service boundary, so values here are trusted enums.
func adminEventClause(status, moderation string) (string, []any) {
	conds := []string{"deleted_at IS NULL"}
	args := []any{}
	if status != "" {
		args = append(args, status)
		conds = append(conds, fmt.Sprintf("status = $%d", len(args)))
	}
	if moderation != "" {
		args = append(args, moderation)
		conds = append(conds, fmt.Sprintf("moderation_status = $%d", len(args)))
	}
	return strings.Join(conds, " AND "), args
}

// AdminList returns every event matching the optional status/moderation
// filters, newest first. Unlike ListVisible there is no public published /
// not-blocked restriction — the admin queue sees drafts and blocked events.
func (r *EventRepository) AdminList(ctx context.Context, status, moderation string, limit, offset int) ([]*domain.Event, error) {
	where, args := adminEventClause(status, moderation)
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+eventColumns+`
		FROM events
		WHERE `+where+`
		ORDER BY starts_at DESC, id
		LIMIT $`+numArg(len(args)+1)+` OFFSET $`+numArg(len(args)+2),
		append(args, limit, offset)...)
	if err != nil {
		return nil, fmt.Errorf("admin list events: %w", err)
	}
	defer rows.Close()

	var events []*domain.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// AdminCount counts events matching the optional admin filters.
func (r *EventRepository) AdminCount(ctx context.Context, status, moderation string) (int, error) {
	where, args := adminEventClause(status, moderation)
	var total int
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) FROM events WHERE `+where, args...).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("admin count events: %w", err)
	}
	return total, nil
}

// ListByVenue returns published events at a venue (used by venue detail).
func (r *EventRepository) ListByVenue(ctx context.Context, venueID string, limit, offset int) ([]*domain.Event, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+eventColumns+`
		FROM events
		WHERE venue_id = $1 AND status = 'published' AND moderation_status <> 'blocked' AND deleted_at IS NULL
		ORDER BY starts_at, id
		LIMIT $2 OFFSET $3`, venueID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list venue events: %w", err)
	}
	defer rows.Close()

	var events []*domain.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// CountByVenue returns the number of published events at a venue.
func (r *EventRepository) CountByVenue(ctx context.Context, venueID string) (int, error) {
	var total int
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) FROM events
		WHERE venue_id = $1 AND status = 'published' AND moderation_status <> 'blocked' AND deleted_at IS NULL`,
		venueID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("count venue events: %w", err)
	}
	return total, nil
}

// eventSearchClause renders the WHERE clause and positional args for an event
// search. Only applied filters are bound; RLS still gates row visibility.
func eventSearchClause(f domain.EventFilters) (string, []any, bool) {
	conds := []string{"e.status = 'published'", "e.moderation_status <> 'blocked'", "e.deleted_at IS NULL"}
	var args []any

	hasQuery := false
	if q := strings.TrimSpace(f.Query); q != "" {
		hasQuery = true
		args = append(args, q)
		conds = append(conds, "e.search_vector @@ plainto_tsquery('english', $"+numArg(len(args))+")")
	}
	if f.CategoryID != "" {
		args = append(args, f.CategoryID)
		conds = append(conds, "e.category_id = $"+numArg(len(args)))
	}
	if f.DateFrom != nil {
		args = append(args, *f.DateFrom)
		conds = append(conds, "e.starts_at >= $"+numArg(len(args)))
	}
	if f.DateTo != nil {
		args = append(args, *f.DateTo)
		conds = append(conds, "e.starts_at <= $"+numArg(len(args)))
	}
	if f.Latitude != nil && f.Longitude != nil && f.RadiusKM != nil {
		args = append(args, *f.Latitude, *f.Longitude, *f.RadiusKM)
		lat, lng, radius := numArg(len(args)-2), numArg(len(args)-1), numArg(len(args))
		conds = append(conds, "6371 * 2 * asin(sqrt(pow(sin((radians(v.latitude) - radians($"+lat+")) / 2), 2)"+
			" + cos(radians($"+lat+")) * cos(radians(v.latitude))* pow(sin((radians(v.longitude) - radians($"+lng+")) / 2), 2))) <= $"+radius)
	}
	return strings.Join(conds, " AND "), args, hasQuery
}

// eventSearchOrder returns the ORDER BY expression: relevance-first when a text
// query is present, else plain time ordering.
func eventSearchOrder(hasQuery bool) string {
	if hasQuery {
		return "ts_rank_cd(search_vector, plainto_tsquery('english', $1)) DESC,"
	}
	return ""
}

func numArg(n int) string {
	return strconv.Itoa(n)
}

func (r *EventRepository) Update(ctx context.Context, e *domain.Event) (*domain.Event, error) {
	return scanEvent(r.q(ctx).QueryRow(ctx, `
		UPDATE events SET
			venue_id = $2, category_id = $3, title = $4, description = $5, starts_at = $6, ends_at = $7,
			price_is_free = $8, price_display = $9, action_type = $10, action_target = $11, max_attendees = $12,
			poster_media_id = $13, teaser_media_id = $14,
			updated_at = now()
		WHERE id = $1
		RETURNING `+eventColumns,
		e.ID, e.VenueID, e.CategoryID, e.Title, e.Description, e.StartsAt, e.EndsAt,
		e.PriceIsFree, e.PriceDisplay, e.ActionType, e.ActionTarget, e.MaxAttendees,
		e.PosterMediaID, e.TeaserMediaID))
}

func (r *EventRepository) UpdateStatus(ctx context.Context, id, status string) error {
	_, err := r.q(ctx).Exec(ctx, `
		UPDATE events SET status = $2, updated_at = now() WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("update event status: %w", err)
	}
	return nil
}
