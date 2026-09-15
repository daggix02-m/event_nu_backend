package service

import (
	"context"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
)

// recapStore is the recap-repository surface RecapService needs.
type recapStore interface {
	ActivityAt(ctx context.Context, eventID string) (time.Time, error)
	GetCached(ctx context.Context, eventID string) (*domain.EventRecap, error)
	CachePut(ctx context.Context, eventID string, data domain.RecapData) error
	Aggregate(ctx context.Context, eventID string) (*domain.RecapData, error)
}

type RecapService struct {
	recaps recapStore
	events eventStore
}

func NewRecapService(recaps recapStore, events eventStore) *RecapService {
	return &RecapService{recaps: recaps, events: events}
}

// Get returns the recap for a publicly visible event. The cached row is served
// while its generated_at is at or after the newest source activity; any source
// change (new moment/review/session/RSVP) makes it stale and triggers a
// regeneration. Invisible events map to ErrNotFound via events.GetByID.
func (s *RecapService) Get(ctx context.Context, eventID string) (*domain.RecapResult, error) {
	if _, err := s.events.GetByID(ctx, eventID); err != nil {
		return nil, err
	}

	activity, err := s.recaps.ActivityAt(ctx, eventID)
	if err != nil {
		return nil, err
	}
	cached, err := s.recaps.GetCached(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if cached != nil && !cached.GeneratedAt.Before(activity) {
		return &domain.RecapResult{Data: cached.Data, GeneratedAt: cached.GeneratedAt, Cached: true}, nil
	}

	data, err := s.recaps.Aggregate(ctx, eventID)
	if err != nil {
		return nil, err
	}
	generatedAt := time.Now().UTC()
	if err := s.recaps.CachePut(ctx, eventID, *data); err != nil {
		return nil, err
	}
	return &domain.RecapResult{Data: *data, GeneratedAt: generatedAt}, nil
}
