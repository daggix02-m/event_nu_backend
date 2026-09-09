package service

import (
	"context"
	"net/http"
	"strings"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/daggix02-m/event_nu/backend/internal/validator"
)

// VenueService covers the public venue detail endpoint and organizer-owned
// venue updates.
type VenueService struct {
	venues venueStore
	events eventStore
	orgs   organizerStore
}

func NewVenueService(venues venueStore, events eventStore, orgs organizerStore) *VenueService {
	return &VenueService{venues: venues, events: events, orgs: orgs}
}

// RequireOrganizer resolves the caller's active organizer, or a 403.
func (s *VenueService) RequireOrganizer(ctx context.Context, userID string) (*domain.Organizer, error) {
	org, err := s.orgs.GetOrganizerByOwner(ctx, userID)
	if err != nil {
		if err == shared.ErrNotFound {
			return nil, shared.NewAppError("organizer_required", "You need an approved organizer account.", http.StatusForbidden)
		}
		return nil, err
	}
	if org.Status != "active" {
		return nil, shared.NewAppError("organizer_required", "Your organizer account is not active.", http.StatusForbidden)
	}
	return org, nil
}

// Detail returns a publicly visible venue plus its published events.
func (s *VenueService) Detail(ctx context.Context, venueID string, page, limit int) (*domain.Venue, PageResult[*domain.Event], error) {
	venue, err := s.venues.GetByID(ctx, venueID)
	if err != nil {
		return nil, PageResult[*domain.Event]{}, err
	}
	offset := (page - 1) * limit
	items, err := s.events.ListByVenue(ctx, venueID, limit, offset)
	if err != nil {
		return nil, PageResult[*domain.Event]{}, err
	}
	total, err := s.events.CountByVenue(ctx, venueID)
	if err != nil {
		return nil, PageResult[*domain.Event]{}, err
	}
	return venue, PageResult[*domain.Event]{Items: items, Total: total}, nil
}

// Update edits a venue owned by the caller's organizer.
func (s *VenueService) Update(ctx context.Context, userID, venueID string, v *domain.Venue) (*domain.Venue, error) {
	org, err := s.RequireOrganizer(ctx, userID)
	if err != nil {
		return nil, err
	}
	existing, err := s.venues.GetByID(ctx, venueID)
	if err != nil {
		return nil, err
	}
	if existing.OrganizerID == nil || *existing.OrganizerID != org.ID {
		return nil, shared.NewAppError("forbidden", "You can only edit your own venues.", http.StatusForbidden)
	}

	val := validator.New()
	val.Required(v.Name, "name")
	val.MaxChars(v.Name, 200, "name")
	val.MaxChars(v.Address, 500, "address")
	val.MaxChars(v.City, 120, "city")
	val.MaxChars(v.CountryCode, 2, "country_code")
	val.MaxChars(v.PlaceID, 200, "place_id")
	val.Check(v.Latitude >= -90 && v.Latitude <= 90, "latitude", "must be between -90 and 90")
	val.Check(v.Longitude >= -180 && v.Longitude <= 180, "longitude", "must be between -180 and 180")
	if !val.Valid() {
		return nil, shared.NewAppError("validation_error", firstFieldError(val), http.StatusUnprocessableEntity)
	}

	v.ID = existing.ID
	v.OrganizerID = existing.OrganizerID
	v.Name = strings.TrimSpace(v.Name)
	return s.venues.Update(ctx, v)
}
