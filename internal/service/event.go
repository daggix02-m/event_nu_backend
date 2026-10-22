package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/domain"
	"github.com/daggix02-m/event_nu_backend/internal/shared"
	"github.com/daggix02-m/event_nu_backend/internal/validator"
)

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
	ListByOrganizer(ctx context.Context, organizerID string, status string, limit, offset int) ([]*domain.Event, error)
	CountByOrganizerFiltered(ctx context.Context, organizerID string, status string) (int, error)
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

type mediaCheckStore interface {
	GetAssetByID(ctx context.Context, id string) (*domain.MediaAsset, error)
}

type EventService struct {
	events eventStore
	venues venueStore
	cats   categoryStore
	orgs   organizerStore
	media  mediaCheckStore
	store  domain.DomainEventWriter
}

func NewEventService(events eventStore, venues venueStore, cats categoryStore, orgs organizerStore) *EventService {
	return &EventService{events: events, venues: venues, cats: cats, orgs: orgs}
}

func (s *EventService) SetMediaStore(media mediaCheckStore) {
	s.media = media
}

// SetDomainEventStore wires the transactional outbox (nil = no fan-out).
// main is the composition root; the service treats the write as a plain
// side-effect of the same transaction that published the event.
func (s *EventService) SetDomainEventStore(store domain.DomainEventWriter) {
	s.store = store
}

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
	if e.PosterMediaID != nil {
		if err := s.requireMedia(ctx, userID, *e.PosterMediaID, domain.MediaKindEventPoster); err != nil {
			return nil, err
		}
	}
	if e.TeaserMediaID != nil {
		if err := s.requireMedia(ctx, userID, *e.TeaserMediaID, domain.MediaKindEventTeaser); err != nil {
			return nil, err
		}
	}
	e.ID = existing.ID
	return s.events.Update(ctx, e)
}

func (s *EventService) requireMedia(ctx context.Context, userID, mediaID, kind string) error {
	if s.media == nil {
		return shared.NewAppError("media_unavailable", "Media attachments are not available.", http.StatusUnprocessableEntity)
	}
	asset, err := s.media.GetAssetByID(ctx, mediaID)
	if err != nil {
		if err == shared.ErrNotFound {
			return shared.NewAppError("invalid_media", "The referenced media asset does not exist.", http.StatusUnprocessableEntity)
		}
		return err
	}
	if asset.UploaderID != userID {
		return shared.NewAppError("invalid_media", "You can only attach your own media.", http.StatusUnprocessableEntity)
	}
	if asset.Kind != kind {
		return shared.NewAppError("invalid_media", "The media asset is not the right kind for this field.", http.StatusUnprocessableEntity)
	}
	if asset.Status != domain.MediaStatusReady {
		return shared.NewAppError("invalid_media", "The media asset has not finished processing.", http.StatusUnprocessableEntity)
	}
	return nil
}

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
	if err := s.emitPublished(ctx, existing, org.ID, userID); err != nil {
		return nil, err
	}
	return existing, nil
}

// emitPublished records the publish fact in the transactional outbox —
// synchronously, inside the caller's transaction. A failure propagates and
// rolls the whole request back: either the aggregate changed AND its fact
// exists after commit, or neither does — no silently dropped facts. The
// relay worker turns the row into Kafka delivery later (at-least-once,
// ordered per key).
func (s *EventService) emitPublished(ctx context.Context, e *domain.Event, organizerID, userID string) error {
	if s.store == nil {
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"event_id":     e.ID,
		"organizer_id": organizerID,
		"published_at": time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	ev := domain.DomainEvent{
		Name:       "event.published",
		Key:        e.ID,
		OccurredAt: time.Now().UTC(),
		Payload:    payload,
	}
	if err := s.store.Write(ctx, ev, userID); err != nil {
		return fmt.Errorf("record %s fact: %w", ev.Name, err)
	}
	return nil
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

func (s *EventService) ListMyEvents(ctx context.Context, userID, status string, page, limit int) (PageResult[*domain.Event], error) {
	org, err := s.orgs.GetOrganizerByOwner(ctx, userID)
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return PageResult[*domain.Event]{Items: []*domain.Event{}, Total: 0}, nil
		}
		return PageResult[*domain.Event]{}, err
	}
	offset := (page - 1) * limit
	items, err := s.events.ListByOrganizer(ctx, org.ID, status, limit, offset)
	if err != nil {
		return PageResult[*domain.Event]{}, err
	}
	total, err := s.events.CountByOrganizerFiltered(ctx, org.ID, status)
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
