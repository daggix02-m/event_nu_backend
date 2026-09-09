package service

import (
	"context"
	"net/http"
	"strings"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/daggix02-m/event_nu/backend/internal/validator"
)

// The stores below are the repository surfaces EventService depends on, kept
// as interfaces (mirroring AuthService.userStore) so unit tests can stub them
// without a database. The concrete repositories satisfy them implicitly.
type eventStore interface {
	GetByID(ctx context.Context, id string) (*domain.Event, error)
	Create(ctx context.Context, e *domain.Event) (*domain.Event, error)
	Update(ctx context.Context, e *domain.Event) (*domain.Event, error)
	UpdateStatus(ctx context.Context, id, status string) error
	SetModeration(ctx context.Context, id, moderation string) error
	ListVisible(ctx context.Context, filters domain.EventFilters, limit, offset int) ([]*domain.Event, error)
	CountVisible(ctx context.Context, filters domain.EventFilters) (int, error)
	ListByVenue(ctx context.Context, venueID string, limit, offset int) ([]*domain.Event, error)
	CountByVenue(ctx context.Context, venueID string) (int, error)
}

type venueStore interface {
	Create(ctx context.Context, v *domain.Venue) (*domain.Venue, error)
	GetByID(ctx context.Context, id string) (*domain.Venue, error)
	Update(ctx context.Context, v *domain.Venue) (*domain.Venue, error)
	ListActive(ctx context.Context, limit, offset int) ([]*domain.Venue, error)
	CountActive(ctx context.Context) (int, error)
}

type categoryStore interface {
	List(ctx context.Context) ([]*domain.Category, error)
}

type EventService struct {
	events eventStore
	venues venueStore
	cats   categoryStore
	orgs   organizerStore
}

func NewEventService(events eventStore, venues venueStore, cats categoryStore, orgs organizerStore) *EventService {
	return &EventService{events: events, venues: venues, cats: cats, orgs: orgs}
}

// RequireOrganizer resolves the caller's active organizer, or a 403.
func (s *EventService) RequireOrganizer(ctx context.Context, userID string) (*domain.Organizer, error) {
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

// CreateVenue registers a venue owned by the caller's organizer.
func (s *EventService) CreateVenue(ctx context.Context, userID string, v *domain.Venue) (*domain.Venue, error) {
	org, err := s.RequireOrganizer(ctx, userID)
	if err != nil {
		return nil, err
	}

	val := validator.New()
	val.Required(v.Name, "name")
	val.MaxChars(v.Name, 200, "name")
	val.Check(v.Latitude >= -90 && v.Latitude <= 90, "latitude", "must be between -90 and 90")
	val.Check(v.Longitude >= -180 && v.Longitude <= 180, "longitude", "must be between -180 and 180")
	if !val.Valid() {
		return nil, shared.NewAppError("validation_error", firstFieldError(val), http.StatusUnprocessableEntity)
	}

	v.OrganizerID = &org.ID
	return s.venues.Create(ctx, v)
}

// PageResult holds a page of items plus total count.
type PageResult[T any] struct {
	Items []T
	Total int
}

func (s *EventService) ListVenues(ctx context.Context, page, limit int) (PageResult[*domain.Venue], error) {
	offset := (page - 1) * limit
	items, err := s.venues.ListActive(ctx, limit, offset)
	if err != nil {
		return PageResult[*domain.Venue]{}, err
	}
	total, err := s.venues.CountActive(ctx)
	if err != nil {
		return PageResult[*domain.Venue]{}, err
	}
	return PageResult[*domain.Venue]{Items: items, Total: total}, nil
}

func (s *EventService) ListCategories(ctx context.Context) ([]*domain.Category, error) {
	return s.cats.List(ctx)
}

// CreateEvent creates a draft event for the caller's organizer.
func (s *EventService) CreateEvent(ctx context.Context, userID string, e *domain.Event) (*domain.Event, error) {
	org, err := s.RequireOrganizer(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := validateEvent(e); err != nil {
		return nil, err
	}
	e.OrganizerID = org.ID
	e.Status = "draft"
	return s.events.Create(ctx, e)
}

// UpdateEvent updates an event the caller's organizer owns.
func (s *EventService) UpdateEvent(ctx context.Context, userID, eventID string, e *domain.Event) (*domain.Event, error) {
	org, err := s.RequireOrganizer(ctx, userID)
	if err != nil {
		return nil, err
	}
	existing, err := s.events.GetByID(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if existing.OrganizerID != org.ID {
		return nil, shared.NewAppError("forbidden", "You can only edit your own events.", http.StatusForbidden)
	}
	if existing.Status != "draft" {
		return nil, shared.NewAppError("event_not_draft", "Only draft events can be edited.", http.StatusConflict)
	}
	if err := validateEvent(e); err != nil {
		return nil, err
	}
	e.ID = existing.ID
	return s.events.Update(ctx, e)
}

// Publish transitions a draft event to published. Requires a venue (the spec:
// a published event points to a valid location).
func (s *EventService) Publish(ctx context.Context, userID, eventID string) (*domain.Event, error) {
	org, err := s.RequireOrganizer(ctx, userID)
	if err != nil {
		return nil, err
	}
	existing, err := s.events.GetByID(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if existing.OrganizerID != org.ID {
		return nil, shared.NewAppError("forbidden", "You can only publish your own events.", http.StatusForbidden)
	}
	if existing.Status != "draft" {
		return nil, shared.NewAppError("event_not_draft", "Only draft events can be published.", http.StatusConflict)
	}
	if existing.VenueID == nil {
		return nil, shared.NewAppError("venue_required", "A venue is required before publishing.", http.StatusUnprocessableEntity)
	}
	if err := s.events.UpdateStatus(ctx, eventID, "published"); err != nil {
		return nil, err
	}
	existing.Status = "published"
	return existing, nil
}

func (s *EventService) GetEvent(ctx context.Context, id string) (*domain.Event, error) {
	return s.events.GetByID(ctx, id)
}

func (s *EventService) ListEvents(ctx context.Context, filters domain.EventFilters, page, limit int) (PageResult[*domain.Event], error) {
	offset := (page - 1) * limit
	items, err := s.events.ListVisible(ctx, filters, limit, offset)
	if err != nil {
		return PageResult[*domain.Event]{}, err
	}
	total, err := s.events.CountVisible(ctx, filters)
	if err != nil {
		return PageResult[*domain.Event]{}, err
	}
	return PageResult[*domain.Event]{Items: items, Total: total}, nil
}

func validateEvent(e *domain.Event) error {
	if e.ActionType == "" {
		e.ActionType = "open_entry"
	}
	v := validator.New()
	v.Required(e.Title, "title")
	v.MaxChars(e.Title, 200, "title")
	v.MaxChars(e.Description, 5000, "description")
	v.Check(!e.StartsAt.IsZero(), "starts_at", "is required")
	if e.EndsAt != nil && e.EndsAt.Before(e.StartsAt) {
		v.AddError("ends_at", "must be after starts_at")
	}
	if e.MaxAttendees != nil && *e.MaxAttendees <= 0 {
		v.AddError("max_attendees", "must be greater than zero")
	}
	if !v.Valid() {
		return shared.NewAppError("validation_error", firstFieldError(v), http.StatusUnprocessableEntity)
	}
	e.Title = strings.TrimSpace(e.Title)
	return nil
}
