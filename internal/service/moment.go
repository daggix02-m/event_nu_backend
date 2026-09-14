package service

import (
	"context"
	"net/http"
	"strings"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

type momentStore interface {
	Create(ctx context.Context, m *domain.Moment) (*domain.Moment, error)
	GetByID(ctx context.Context, id string) (*domain.Moment, error)
	Delete(ctx context.Context, id string) error
	DeleteAuthorized(ctx context.Context, id string) error
	ListByEvent(ctx context.Context, eventID string, limit, offset int) ([]*domain.Moment, error)
	CountByEvent(ctx context.Context, eventID string) (int, error)
	HasRSVPOrTicket(ctx context.Context, userID, eventID string) (bool, error)
	IsValidMediaAsset(ctx context.Context, userID, mediaAssetID string) (bool, error)
	ListAttendees(ctx context.Context, eventID string, limit, offset int) ([]*domain.MomentAttendee, error)
	CountAttendees(ctx context.Context, eventID string) (int, error)
}

type MomentService struct {
	moments momentStore
	events  eventStore
}

func NewMomentService(moments momentStore, events eventStore) *MomentService {
	return &MomentService{moments: moments, events: events}
}

// Create adds a moment to a published event. The caller must hold an RSVP or
// ticket, and the media asset must belong to them and be ready.
func (s *MomentService) Create(ctx context.Context, userID, eventID, mediaAssetID, caption string) (*domain.Moment, error) {
	caption = strings.TrimSpace(caption)

	if _, err := s.events.GetByID(ctx, eventID); err != nil {
		return nil, err
	}

	eligible, err := s.moments.HasRSVPOrTicket(ctx, userID, eventID)
	if err != nil {
		return nil, err
	}
	if !eligible {
		return nil, shared.NewAppError("forbidden", "You must RSVP or have a ticket to share moments at this event.", http.StatusForbidden)
	}

	valid, err := s.moments.IsValidMediaAsset(ctx, userID, mediaAssetID)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, shared.NewAppError("invalid_media", "Media asset not found or not ready.", http.StatusUnprocessableEntity)
	}

	return s.moments.Create(ctx, &domain.Moment{
		EventID:      eventID,
		UserID:       userID,
		MediaAssetID: mediaAssetID,
		Caption:      caption,
	})
}

func (s *MomentService) List(ctx context.Context, eventID string, page, limit int) (PageResult[*domain.Moment], error) {
	if _, err := s.events.GetByID(ctx, eventID); err != nil {
		return PageResult[*domain.Moment]{}, err
	}
	offset := (page - 1) * limit
	items, err := s.moments.ListByEvent(ctx, eventID, limit, offset)
	if err != nil {
		return PageResult[*domain.Moment]{}, err
	}
	total, err := s.moments.CountByEvent(ctx, eventID)
	if err != nil {
		return PageResult[*domain.Moment]{}, err
	}
	return PageResult[*domain.Moment]{Items: items, Total: total}, nil
}

// Delete removes a moment. The owner may always delete; admins may remove any
// (via the privileged definer helper, since request RLS runs as 'user').
func (s *MomentService) Delete(ctx context.Context, userID, momentID string, admin bool) error {
	existing, err := s.moments.GetByID(ctx, momentID)
	if err != nil {
		return err
	}
	if existing.UserID == userID {
		return s.moments.Delete(ctx, momentID)
	}
	if admin {
		return s.moments.DeleteAuthorized(ctx, momentID)
	}
	return shared.NewAppError("forbidden", "You can only delete your own moments.", http.StatusForbidden)
}

// ListAttendees returns the public_rsvp attendee directory for a published event.
func (s *MomentService) ListAttendees(ctx context.Context, eventID string, page, limit int) (PageResult[*domain.MomentAttendee], error) {
	if _, err := s.events.GetByID(ctx, eventID); err != nil {
		return PageResult[*domain.MomentAttendee]{}, err
	}
	offset := (page - 1) * limit
	items, err := s.moments.ListAttendees(ctx, eventID, limit, offset)
	if err != nil {
		return PageResult[*domain.MomentAttendee]{}, err
	}
	total, err := s.moments.CountAttendees(ctx, eventID)
	if err != nil {
		return PageResult[*domain.MomentAttendee]{}, err
	}
	return PageResult[*domain.MomentAttendee]{Items: items, Total: total}, nil
}
