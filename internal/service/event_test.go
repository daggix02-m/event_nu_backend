package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// stubEventStore implements eventStore with scripted methods so EventService
// can be tested without a database.
type stubEventStore struct {
	onGetByID       func(id string) (*domain.Event, error)
	onCreate        func(e *domain.Event) (*domain.Event, error)
	onUpdate        func(e *domain.Event) (*domain.Event, error)
	onUpdateStatus  func(id, status string) error
	onSetModeration func(id, moderation string) error
	onListVisible   func(filters domain.EventFilters, limit, offset int) ([]*domain.Event, error)
	onCountVisible  func(filters domain.EventFilters) (int, error)
	onListByVenue   func(venueID string, limit, offset int) ([]*domain.Event, error)
	onCountByVenue  func(venueID string) (int, error)
}

func (s stubEventStore) GetByID(ctx context.Context, id string) (*domain.Event, error) {
	if s.onGetByID == nil {
		return nil, shared.ErrNotFound
	}
	return s.onGetByID(id)
}

func (s stubEventStore) Create(ctx context.Context, e *domain.Event) (*domain.Event, error) {
	if s.onCreate == nil {
		return e, nil
	}
	return s.onCreate(e)
}

func (s stubEventStore) Update(ctx context.Context, e *domain.Event) (*domain.Event, error) {
	if s.onUpdate == nil {
		return e, nil
	}
	return s.onUpdate(e)
}

func (s stubEventStore) UpdateStatus(ctx context.Context, id, status string) error {
	if s.onUpdateStatus == nil {
		return nil
	}
	return s.onUpdateStatus(id, status)
}

func (s stubEventStore) SetModeration(ctx context.Context, id, moderation string) error {
	if s.onSetModeration == nil {
		return nil
	}
	return s.onSetModeration(id, moderation)
}

func (s stubEventStore) ListVisible(ctx context.Context, filters domain.EventFilters, limit, offset int) ([]*domain.Event, error) {
	if s.onListVisible == nil {
		return nil, nil
	}
	return s.onListVisible(filters, limit, offset)
}

func (s stubEventStore) CountVisible(ctx context.Context, filters domain.EventFilters) (int, error) {
	if s.onCountVisible == nil {
		return 0, nil
	}
	return s.onCountVisible(filters)
}

func (s stubEventStore) ListByVenue(ctx context.Context, venueID string, limit, offset int) ([]*domain.Event, error) {
	if s.onListByVenue == nil {
		return nil, nil
	}
	return s.onListByVenue(venueID, limit, offset)
}

func (s stubEventStore) CountByVenue(ctx context.Context, venueID string) (int, error) {
	if s.onCountByVenue == nil {
		return 0, nil
	}
	return s.onCountByVenue(venueID)
}

type stubVenueStore struct {
	onGetByID     func(id string) (*domain.Venue, error)
	onCreate      func(v *domain.Venue) (*domain.Venue, error)
	onUpdate      func(v *domain.Venue) (*domain.Venue, error)
	onListActive  func(limit, offset int) ([]*domain.Venue, error)
	onCountActive func() (int, error)
}

func (s stubVenueStore) GetByID(ctx context.Context, id string) (*domain.Venue, error) {
	if s.onGetByID == nil {
		return nil, shared.ErrNotFound
	}
	return s.onGetByID(id)
}

func (s stubVenueStore) Create(ctx context.Context, v *domain.Venue) (*domain.Venue, error) {
	if s.onCreate == nil {
		return v, nil
	}
	return s.onCreate(v)
}

func (s stubVenueStore) Update(ctx context.Context, v *domain.Venue) (*domain.Venue, error) {
	if s.onUpdate == nil {
		return v, nil
	}
	return s.onUpdate(v)
}

func (s stubVenueStore) ListActive(ctx context.Context, limit, offset int) ([]*domain.Venue, error) {
	if s.onListActive == nil {
		return nil, nil
	}
	return s.onListActive(limit, offset)
}

func (s stubVenueStore) CountActive(ctx context.Context) (int, error) {
	if s.onCountActive == nil {
		return 0, nil
	}
	return s.onCountActive()
}

type stubCategoryStore struct {
	onList func() ([]*domain.Category, error)
}

func (s stubCategoryStore) List(ctx context.Context) ([]*domain.Category, error) {
	if s.onList == nil {
		return nil, nil
	}
	return s.onList()
}

func newEventService() *EventService {
	return NewEventService(stubEventStore{}, stubVenueStore{}, stubCategoryStore{}, stubOrganizerStore{
		onGetOrganizerByOwner: func(userID string) (*domain.Organizer, error) {
			return activeOrganizer(), nil
		},
	})
}

func draftEvent() *domain.Event {
	now := time.Now().Add(time.Hour)
	return &domain.Event{Title: "Salsa Night", StartsAt: now, Status: "draft"}
}

func venueArg() *domain.Venue {
	return &domain.Venue{Name: "Main Hall", Latitude: 40.7, Longitude: -74.0}
}

func TestRequireOrganizerActive(t *testing.T) {
	s := newEventService()
	org, err := s.RequireOrganizer(t.Context(), "u-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if org.ID != "org-1" || org.Status != "active" || org.Name != "Acme" {
		t.Fatalf("expected the active organizer, got %+v", org)
	}
}

func TestRequireOrganizerMissingMapsTo403(t *testing.T) {
	s := NewEventService(stubEventStore{}, stubVenueStore{}, stubCategoryStore{}, stubOrganizerStore{})
	_, err := s.RequireOrganizer(t.Context(), "u-9")
	assertAppError(t, err, "organizer_required", http.StatusForbidden)
}

func TestRequireOrganizerInactiveMapsTo403(t *testing.T) {
	org := activeOrganizer()
	org.Status = "suspended"
	s := NewEventService(stubEventStore{}, stubVenueStore{}, stubCategoryStore{}, stubOrganizerStore{
		onGetOrganizerByOwner: func(userID string) (*domain.Organizer, error) { return org, nil },
	})
	_, err := s.RequireOrganizer(t.Context(), "u-1")
	assertAppError(t, err, "organizer_required", http.StatusForbidden)
}

func TestRequireOrganizerPropagatesStoreError(t *testing.T) {
	boom := errors.New("store down")
	s := NewEventService(stubEventStore{}, stubVenueStore{}, stubCategoryStore{}, stubOrganizerStore{
		onGetOrganizerByOwner: func(userID string) (*domain.Organizer, error) { return nil, boom },
	})
	if _, err := s.RequireOrganizer(t.Context(), "u-1"); !errors.Is(err, boom) {
		t.Fatalf("expected store error to propagate, got %v", err)
	}
}

func TestCreateVenueValidation(t *testing.T) {
	tests := []struct {
		name string
		arg  *domain.Venue
	}{
		{name: "empty name", arg: &domain.Venue{Latitude: 1, Longitude: 1}},
		{name: "long name", arg: &domain.Venue{Name: strings.Repeat("n", 201), Latitude: 1, Longitude: 1}},
		{name: "latitude too low", arg: &domain.Venue{Name: "Hall", Latitude: -91, Longitude: 1}},
		{name: "latitude too high", arg: &domain.Venue{Name: "Hall", Latitude: 91, Longitude: 1}},
		{name: "longitude too low", arg: &domain.Venue{Name: "Hall", Latitude: 1, Longitude: -181}},
		{name: "longitude too high", arg: &domain.Venue{Name: "Hall", Latitude: 1, Longitude: 181}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newEventService()
			_, err := s.CreateVenue(t.Context(), "u-1", tt.arg)
			assertAppError(t, err, "validation_error", http.StatusUnprocessableEntity)
		})
	}
}

func TestCreateVenueRequiresOrganizer(t *testing.T) {
	s := NewEventService(stubEventStore{}, stubVenueStore{}, stubCategoryStore{}, stubOrganizerStore{})
	_, err := s.CreateVenue(t.Context(), "u-9", venueArg())
	assertAppError(t, err, "organizer_required", http.StatusForbidden)
}

func TestCreateVenueSetsOrganizerID(t *testing.T) {
	var created *domain.Venue
	s := NewEventService(stubEventStore{}, stubVenueStore{
		onCreate: func(v *domain.Venue) (*domain.Venue, error) {
			created = v
			return v, nil
		},
	}, stubCategoryStore{}, stubOrganizerStore{
		onGetOrganizerByOwner: func(userID string) (*domain.Organizer, error) { return activeOrganizer(), nil },
	})
	got, err := s.CreateVenue(t.Context(), "u-1", venueArg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.OrganizerID == nil || *created.OrganizerID != "org-1" {
		t.Fatalf("expected venue owned by org-1, got %v", created)
	}
	if got != created {
		t.Fatal("expected the created venue to be returned")
	}
}

func TestListVenuesComputesOffsetAndPropagates(t *testing.T) {
	var gotLimit, gotOffset int
	venues := []*domain.Venue{{Name: "A"}, {Name: "B"}}
	s := NewEventService(stubEventStore{}, stubVenueStore{
		onListActive: func(limit, offset int) ([]*domain.Venue, error) {
			gotLimit, gotOffset = limit, offset
			return venues, nil
		},
		onCountActive: func() (int, error) { return 42, nil },
	}, stubCategoryStore{}, stubOrganizerStore{})

	res, err := s.ListVenues(t.Context(), 3, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotLimit != 10 || gotOffset != 20 {
		t.Fatalf("expected limit/offset 10/20, got %d/%d", gotLimit, gotOffset)
	}
	if res.Total != 42 || len(res.Items) != 2 {
		t.Fatalf("unexpected page result: total=%d items=%d", res.Total, len(res.Items))
	}
}

func TestListVenuesPropagatesCountError(t *testing.T) {
	boom := errors.New("count failed")
	s := NewEventService(stubEventStore{}, stubVenueStore{
		onListActive:  func(limit, offset int) ([]*domain.Venue, error) { return nil, nil },
		onCountActive: func() (int, error) { return 0, boom },
	}, stubCategoryStore{}, stubOrganizerStore{})
	if _, err := s.ListVenues(t.Context(), 1, 10); !errors.Is(err, boom) {
		t.Fatalf("expected count error to propagate, got %v", err)
	}
}

func TestListCategories(t *testing.T) {
	cats := []*domain.Category{{Slug: "music"}}
	s := NewEventService(stubEventStore{}, stubVenueStore{}, stubCategoryStore{
		onList: func() ([]*domain.Category, error) { return cats, nil },
	}, stubOrganizerStore{})
	got, err := s.ListCategories(t.Context())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Slug != "music" {
		t.Fatalf("unexpected categories: %v", got)
	}
}

func TestCreateEventDefaultsAndSetsDraft(t *testing.T) {
	var created *domain.Event
	s := NewEventService(stubEventStore{
		onCreate: func(e *domain.Event) (*domain.Event, error) {
			created = e
			return e, nil
		},
	}, stubVenueStore{}, stubCategoryStore{}, stubOrganizerStore{
		onGetOrganizerByOwner: func(userID string) (*domain.Organizer, error) { return activeOrganizer(), nil },
	})

	arg := draftEvent()
	arg.ActionType = ""
	got, err := s.CreateEvent(t.Context(), "u-1", arg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.ActionType != "open_entry" {
		t.Fatalf("expected default action type open_entry, got %q", created.ActionType)
	}
	if created.Status != "draft" {
		t.Fatalf("expected draft status, got %q", created.Status)
	}
	if created.OrganizerID != "org-1" {
		t.Fatalf("expected event owned by org-1, got %q", created.OrganizerID)
	}
	if got != created {
		t.Fatal("expected the created event to be returned")
	}
}

func TestCreateEventValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(e *domain.Event)
	}{
		{name: "empty title", mutate: func(e *domain.Event) { e.Title = "" }},
		{name: "long title", mutate: func(e *domain.Event) { e.Title = strings.Repeat("t", 201) }},
		{name: "missing start", mutate: func(e *domain.Event) { e.StartsAt = time.Time{} }},
		{name: "ends before start", mutate: func(e *domain.Event) { past := time.Unix(0, 0); e.EndsAt = &past }},
		{name: "zero attendees", mutate: func(e *domain.Event) { n := 0; e.MaxAttendees = &n }},
		{name: "negative attendees", mutate: func(e *domain.Event) { n := -3; e.MaxAttendees = &n }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newEventService()
			e := draftEvent()
			tt.mutate(e)
			_, err := s.CreateEvent(t.Context(), "u-1", e)
			assertAppError(t, err, "validation_error", http.StatusUnprocessableEntity)
		})
	}
}

func TestUpdateEventRejectsNonOwner(t *testing.T) {
	existing := draftEvent()
	existing.ID = "e-1"
	existing.OrganizerID = "org-other"
	s := NewEventService(stubEventStore{
		onGetByID: func(id string) (*domain.Event, error) { return existing, nil },
	}, stubVenueStore{}, stubCategoryStore{}, stubOrganizerStore{
		onGetOrganizerByOwner: func(userID string) (*domain.Organizer, error) { return activeOrganizer(), nil },
	})
	_, err := s.UpdateEvent(t.Context(), "u-1", "e-1", draftEvent())
	assertAppError(t, err, "forbidden", http.StatusForbidden)
}

func TestUpdateEventRejectsPublished(t *testing.T) {
	existing := draftEvent()
	existing.ID = "e-1"
	existing.OrganizerID = "org-1"
	existing.Status = "published"
	s := NewEventService(stubEventStore{
		onGetByID: func(id string) (*domain.Event, error) { return existing, nil },
	}, stubVenueStore{}, stubCategoryStore{}, stubOrganizerStore{
		onGetOrganizerByOwner: func(userID string) (*domain.Organizer, error) { return activeOrganizer(), nil },
	})
	_, err := s.UpdateEvent(t.Context(), "u-1", "e-1", draftEvent())
	assertAppError(t, err, "event_not_draft", http.StatusConflict)
}

func TestUpdateEventSuccessKeepsExistingID(t *testing.T) {
	existing := draftEvent()
	existing.ID = "e-1"
	existing.OrganizerID = "org-1"
	existing.Status = "draft"
	var updated *domain.Event
	s := NewEventService(stubEventStore{
		onGetByID: func(id string) (*domain.Event, error) { return existing, nil },
		onUpdate: func(e *domain.Event) (*domain.Event, error) {
			updated = e
			return e, nil
		},
	}, stubVenueStore{}, stubCategoryStore{}, stubOrganizerStore{
		onGetOrganizerByOwner: func(userID string) (*domain.Organizer, error) { return activeOrganizer(), nil },
	})
	arg := draftEvent()
	arg.Title = "Renamed  "
	got, err := s.UpdateEvent(t.Context(), "u-1", "e-1", arg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.ID != "e-1" {
		t.Fatalf("expected existing event id to be preserved, got %q", updated.ID)
	}
	if got != updated {
		t.Fatal("expected the updated event to be returned")
	}
}

func TestPublishRejectsNonOwnerAndNonDraft(t *testing.T) {
	t.Run("non owner", func(t *testing.T) {
		existing := draftEvent()
		existing.ID = "e-1"
		existing.OrganizerID = "org-other"
		existing.Status = "draft"
		s := NewEventService(stubEventStore{
			onGetByID: func(id string) (*domain.Event, error) { return existing, nil },
		}, stubVenueStore{}, stubCategoryStore{}, stubOrganizerStore{
			onGetOrganizerByOwner: func(userID string) (*domain.Organizer, error) { return activeOrganizer(), nil },
		})
		_, err := s.Publish(t.Context(), "u-1", "e-1")
		assertAppError(t, err, "forbidden", http.StatusForbidden)
	})

	t.Run("non draft", func(t *testing.T) {
		existing := draftEvent()
		existing.ID = "e-1"
		existing.OrganizerID = "org-1"
		existing.Status = "published"
		s := NewEventService(stubEventStore{
			onGetByID: func(id string) (*domain.Event, error) { return existing, nil },
		}, stubVenueStore{}, stubCategoryStore{}, stubOrganizerStore{
			onGetOrganizerByOwner: func(userID string) (*domain.Organizer, error) { return activeOrganizer(), nil },
		})
		_, err := s.Publish(t.Context(), "u-1", "e-1")
		assertAppError(t, err, "event_not_draft", http.StatusConflict)
	})
}

func TestPublishRequiresVenue(t *testing.T) {
	existing := draftEvent()
	existing.ID = "e-1"
	existing.OrganizerID = "org-1"
	existing.Status = "draft"
	existing.VenueID = nil
	s := NewEventService(stubEventStore{
		onGetByID: func(id string) (*domain.Event, error) { return existing, nil },
	}, stubVenueStore{}, stubCategoryStore{}, stubOrganizerStore{
		onGetOrganizerByOwner: func(userID string) (*domain.Organizer, error) { return activeOrganizer(), nil },
	})
	_, err := s.Publish(t.Context(), "u-1", "e-1")
	assertAppError(t, err, "venue_required", http.StatusUnprocessableEntity)
}

func TestPublishSuccess(t *testing.T) {
	existing := draftEvent()
	existing.ID = "e-1"
	existing.OrganizerID = "org-1"
	existing.Status = "draft"
	venue := "v-1"
	existing.VenueID = &venue
	var statusID, status string
	s := NewEventService(stubEventStore{
		onGetByID: func(id string) (*domain.Event, error) { return existing, nil },
		onUpdateStatus: func(id, st string) error {
			statusID, status = id, st
			return nil
		},
	}, stubVenueStore{}, stubCategoryStore{}, stubOrganizerStore{
		onGetOrganizerByOwner: func(userID string) (*domain.Organizer, error) { return activeOrganizer(), nil },
	})
	got, err := s.Publish(t.Context(), "u-1", "e-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if statusID != "e-1" || status != "published" {
		t.Fatalf("expected status update to published for e-1, got %q/%q", statusID, status)
	}
	if got.Status != "published" {
		t.Fatalf("expected returned event to be published, got %q", got.Status)
	}
}

func TestGetAndListEvents(t *testing.T) {
	t.Run("get by id", func(t *testing.T) {
		e := draftEvent()
		s := NewEventService(stubEventStore{
			onGetByID: func(id string) (*domain.Event, error) { return e, nil },
		}, stubVenueStore{}, stubCategoryStore{}, stubOrganizerStore{})
		got, err := s.GetEvent(t.Context(), "e-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != e {
			t.Fatal("expected stubbed event back")
		}
	})

	t.Run("list computes offset", func(t *testing.T) {
		var gotLimit, gotOffset int
		s := NewEventService(stubEventStore{
			onListVisible: func(f domain.EventFilters, limit, offset int) ([]*domain.Event, error) {
				gotLimit, gotOffset = limit, offset
				return []*domain.Event{draftEvent()}, nil
			},
			onCountVisible: func(f domain.EventFilters) (int, error) { return 7, nil },
		}, stubVenueStore{}, stubCategoryStore{}, stubOrganizerStore{})
		res, err := s.ListEvents(t.Context(), domain.EventFilters{}, 4, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotLimit != 5 || gotOffset != 15 {
			t.Fatalf("expected limit/offset 5/15, got %d/%d", gotLimit, gotOffset)
		}
		if res.Total != 7 || len(res.Items) != 1 {
			t.Fatalf("unexpected page result: total=%d items=%d", res.Total, len(res.Items))
		}
	})
}

func TestValidateEventTrimsTitleAndDefaultsAction(t *testing.T) {
	e := draftEvent()
	e.Title = "  Salsa Night  "
	e.ActionType = ""
	e.MaxAttendees = nil
	if err := validateEvent(e); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.Title != "Salsa Night" {
		t.Fatalf("expected trimmed title, got %q", e.Title)
	}
	if e.ActionType != "open_entry" {
		t.Fatalf("expected default action type, got %q", e.ActionType)
	}
}

func TestValidateEventAcceptsExplicitActionType(t *testing.T) {
	e := draftEvent()
	e.ActionType = "rsvp"
	if err := validateEvent(e); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.ActionType != "rsvp" {
		t.Fatalf("expected explicit action type preserved, got %q", e.ActionType)
	}
}

func TestValidateEventDescriptionsWithinLimit(t *testing.T) {
	e := draftEvent()
	e.Description = strings.Repeat("d", 5000)
	if err := validateEvent(e); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e.Description = strings.Repeat("d", 5001)
	if err := validateEvent(e); err == nil {
		t.Fatal("expected over-long description to be rejected")
	}
}
