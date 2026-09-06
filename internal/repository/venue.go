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

func (r *VenueRepository) ListActive(ctx context.Context) ([]*domain.Venue, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+venueColumns+`
		FROM venues
		WHERE status = 'active' AND deleted_at IS NULL
		ORDER BY name`)
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
