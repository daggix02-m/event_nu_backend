package service

import (
	"context"
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// rsvpStore is the rsvp-repository surface RsvpService needs.
type rsvpStore interface {
	AttemptRSVP(ctx context.Context, eventID, userID string, publicRsvp bool) (string, *domain.Rsvp, error)
	CancelRSVP(ctx context.Context, userID, eventID string) error
	GetState(ctx context.Context, userID, eventID string) (*domain.Rsvp, error)
	ListByUser(ctx context.Context, userID string, limit, offset int) ([]*domain.RsvpWithEvent, error)
	CountByUser(ctx context.Context, userID string) (int, error)
	CountAttended(ctx context.Context, userID, eventID string) (int, error)
}

type RsvpService struct {
	rsvps    rsvpStore
	events   eventStore
	notifier Notifier
}

func NewRsvpService(rsvps rsvpStore, events eventStore) *RsvpService {
	return &RsvpService{rsvps: rsvps, events: events}
}

// SetNotifier optionally attaches the notification hook (nil-safe).
func (s *RsvpService) SetNotifier(n Notifier) {
	s.notifier = n
}

// Create reserves a seat for the caller on a publicly visible event. Capacity
// and duplicate detection are atomic inside attempt_event_rsvp() at the SQL
// layer; this method maps the outcome to API errors.
func (s *RsvpService) Create(ctx context.Context, userID, eventID string, publicRsvp bool) (*domain.Rsvp, error) {
	if _, err := s.events.GetByID(ctx, eventID); err != nil {
		return nil, err
	}
	status, rsvp, err := s.rsvps.AttemptRSVP(ctx, eventID, userID, publicRsvp)
	if err != nil {
		return nil, err
	}
	switch status {
	case "ok":
		if s.notifier != nil {
			_ = s.notifier.NotifyEventOrganizer(ctx, eventID, "rsvp", "New RSVP", "")
		}
		return rsvp, nil
	case "duplicate":
		return nil, shared.NewAppError("duplicate_rsvp", "You are already going to this event.", http.StatusConflict)
	case "capacity_full":
		return nil, shared.NewAppError("event_full", "This event is at full capacity.", http.StatusConflict)
	case "forbidden":
		return nil, shared.NewAppError("forbidden", "You can only RSVP for yourself.", http.StatusForbidden)
	default:
		return nil, shared.ErrNotFound
	}
}

// Cancel releases the caller's seat. Idempotent: cancelling without an RSVP is
// a no-op success.
func (s *RsvpService) Cancel(ctx context.Context, userID, eventID string) error {
	return s.rsvps.CancelRSVP(ctx, userID, eventID)
}

// State returns the caller's RSVP for an event, or nil when they are not going.
func (s *RsvpService) State(ctx context.Context, userID, eventID string) (*domain.Rsvp, error) {
	rsvp, err := s.rsvps.GetState(ctx, userID, eventID)
	if err == shared.ErrNotFound {
		return nil, nil
	}
	return rsvp, err
}

func (s *RsvpService) MyRsvps(ctx context.Context, userID string, page, limit int) (PageResult[*domain.RsvpWithEvent], error) {
	offset := (page - 1) * limit
	items, err := s.rsvps.ListByUser(ctx, userID, limit, offset)
	if err != nil {
		return PageResult[*domain.RsvpWithEvent]{}, err
	}
	total, err := s.rsvps.CountByUser(ctx, userID)
	if err != nil {
		return PageResult[*domain.RsvpWithEvent]{}, err
	}
	return PageResult[*domain.RsvpWithEvent]{Items: items, Total: total}, nil
}
