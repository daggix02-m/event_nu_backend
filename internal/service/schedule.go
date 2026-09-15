package service

import (
	"context"
	"net/http"
	"strings"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/daggix02-m/event_nu/backend/internal/validator"
)

type eventSessionStore interface {
	Create(ctx context.Context, s *domain.EventSession) (*domain.EventSession, error)
	GetByID(ctx context.Context, id string) (*domain.EventSession, error)
	Update(ctx context.Context, s *domain.EventSession) (*domain.EventSession, error)
	Delete(ctx context.Context, id string) error
	ListByEvent(ctx context.Context, eventID string) ([]*domain.EventSession, error)
}

type ScheduleService struct {
	sessions eventSessionStore
	events   eventStore
	orgs     organizerStore
}

func NewScheduleService(sessions eventSessionStore, events eventStore, orgs organizerStore) *ScheduleService {
	return &ScheduleService{sessions: sessions, events: events, orgs: orgs}
}

// requireEventOrganizer verifies userID owns an active organizer account that
// manages the given event.
func (s *ScheduleService) requireEventOrganizer(ctx context.Context, userID, eventID string) error {
	org, err := s.orgs.GetOrganizerByOwner(ctx, userID)
	if err != nil {
		if err == shared.ErrNotFound {
			return shared.NewAppError("organizer_required", "You need an approved organizer account.", http.StatusForbidden)
		}
		return err
	}
	if org.Status != "active" {
		return shared.NewAppError("organizer_required", "Your organizer account is not active.", http.StatusForbidden)
	}
	ev, err := s.events.GetByID(ctx, eventID)
	if err != nil {
		return err
	}
	if ev.OrganizerID != org.ID {
		return shared.NewAppError("forbidden", "You can only manage your own events.", http.StatusForbidden)
	}
	return nil
}

func validateSession(s *domain.EventSession) error {
	v := validator.New()
	v.Required(s.Title, "title")
	v.MaxChars(s.Title, 200, "title")
	if s.StartsAt.IsZero() {
		v.AddError("starts_at", "is required")
	}
	if s.EndsAt != nil && !s.StartsAt.IsZero() && s.EndsAt.Before(s.StartsAt) {
		v.AddError("ends_at", "must be on or after starts_at")
	}
	if !v.Valid() {
		return shared.NewAppError("validation_error", firstFieldError(v), http.StatusUnprocessableEntity)
	}
	return nil
}

func sanitizeSession(s *domain.EventSession) {
	s.Title = strings.TrimSpace(s.Title)
	if s.Speaker != nil {
		t := strings.TrimSpace(*s.Speaker)
		s.Speaker = &t
	}
	if s.Stage != nil {
		t := strings.TrimSpace(*s.Stage)
		s.Stage = &t
	}
}

func (s *ScheduleService) Create(ctx context.Context, userID, eventID string, sess *domain.EventSession) (*domain.EventSession, error) {
	if err := s.requireEventOrganizer(ctx, userID, eventID); err != nil {
		return nil, err
	}
	if err := validateSession(sess); err != nil {
		return nil, err
	}
	sess.EventID = eventID
	sanitizeSession(sess)
	return s.sessions.Create(ctx, sess)
}

func (s *ScheduleService) Update(ctx context.Context, userID, sessionID string, sess *domain.EventSession) (*domain.EventSession, error) {
	existing, err := s.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := s.requireEventOrganizer(ctx, userID, existing.EventID); err != nil {
		return nil, err
	}
	if err := validateSession(sess); err != nil {
		return nil, err
	}
	sess.ID = sessionID
	sess.EventID = existing.EventID
	sanitizeSession(sess)
	return s.sessions.Update(ctx, sess)
}

func (s *ScheduleService) Delete(ctx context.Context, userID, sessionID string) error {
	existing, err := s.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if err := s.requireEventOrganizer(ctx, userID, existing.EventID); err != nil {
		return err
	}
	return s.sessions.Delete(ctx, sessionID)
}

func (s *ScheduleService) List(ctx context.Context, eventID string) ([]*domain.EventSession, error) {
	if _, err := s.events.GetByID(ctx, eventID); err != nil {
		return nil, err
	}
	return s.sessions.ListByEvent(ctx, eventID)
}
