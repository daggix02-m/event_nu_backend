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

type VenueRepository struct {
	pool *pgxpool.Pool
}

func NewVenueRepository(pool *pgxpool.Pool) *VenueRepository {
	return &VenueRepository{pool: pool}
}

func (r *VenueRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const venueColumns = "id, organizer_id, name, address, latitude, longitude, place_id, city, country_code, status, created_at, deleted_at"

func scanVenue(row pgx.Row) (*domain.Venue, error) {
	var v domain.Venue
	err := row.Scan(&v.ID, &v.OrganizerID, &v.Name, &v.Address, &v.Latitude, &v.Longitude,
		&v.PlaceID, &v.City, &v.CountryCode, &v.Status, &v.CreatedAt, &v.DeletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan venue: %w", err)
	}
	return &v, nil
}

func (r *VenueRepository) Create(ctx context.Context, v *domain.Venue) (*domain.Venue, error) {
	return scanVenue(r.q(ctx).QueryRow(ctx, `
		INSERT INTO venues (organizer_id, name, address, latitude, longitude, place_id, city, country_code)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+venueColumns,
		v.OrganizerID, v.Name, v.Address, v.Latitude, v.Longitude, v.PlaceID, v.City, v.CountryCode))
}

func (r *VenueRepository) GetByID(ctx context.Context, id string) (*domain.Venue, error) {
	return scanVenue(r.q(ctx).QueryRow(ctx, `
		SELECT `+venueColumns+`
		FROM venues WHERE id = $1 AND deleted_at IS NULL`, id))
}

// Update edits the editable venue fields. Ownership is enforced upstream (the
// venue's organizer must belong to the caller); RLS is the backstop.
func (r *VenueRepository) Update(ctx context.Context, v *domain.Venue) (*domain.Venue, error) {
	return scanVenue(r.q(ctx).QueryRow(ctx, `
		UPDATE venues SET
			name = $2, address = $3, latitude = $4, longitude = $5, place_id = $6,
			city = $7, country_code = $8, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING `+venueColumns,
		v.ID, v.Name, v.Address, v.Latitude, v.Longitude, v.PlaceID, v.City, v.CountryCode))
}

func (r *VenueRepository) ListActive(ctx context.Context, limit, offset int) ([]*domain.Venue, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+venueColumns+`
		FROM venues
		WHERE status = 'active' AND deleted_at IS NULL
		ORDER BY name, id
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list venues: %w", err)
	}
	defer rows.Close()

	var venues []*domain.Venue
	for rows.Next() {
		v, err := scanVenue(rows)
		if err != nil {
			return nil, err
		}
		venues = append(venues, v)
	}
	return venues, rows.Err()
}

// CountActive returns the total number of active venues.
func (r *VenueRepository) CountActive(ctx context.Context) (int, error) {
	var total int
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) FROM venues
		WHERE status = 'active' AND deleted_at IS NULL`).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("count venues: %w", err)
	}
	return total, nil
}
