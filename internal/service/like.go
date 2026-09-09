package service

import (
	"context"
)

// likeStore is the like-repository surface LikeService depends on.
type likeStore interface {
	Add(ctx context.Context, eventID, userID string) error
	Remove(ctx context.Context, eventID, userID string) error
	State(ctx context.Context, eventID, userID string) (int, bool, error)
}

type LikeService struct {
	likes  likeStore
	events eventStore
}

func NewLikeService(likes likeStore, events eventStore) *LikeService {
	return &LikeService{likes: likes, events: events}
}

// Add likes an event, refusing events that are not visible (published,
// non-blocked) under current RLS. Idempotent: a duplicate like is a no-op.
func (s *LikeService) Add(ctx context.Context, userID, eventID string) error {
	if _, err := s.events.GetByID(ctx, eventID); err != nil {
		return err
	}
	return s.likes.Add(ctx, eventID, userID)
}

// Remove unlikes an event. Intentionally skips the visibility check so unliking
// stays idempotent even after an event is cancelled/archived, and never 404s
// merely because the like was already removed.
func (s *LikeService) Remove(ctx context.Context, userID, eventID string) error {
	return s.likes.Remove(ctx, eventID, userID)
}

// State returns the like count and whether the user liked the event.
func (s *LikeService) State(ctx context.Context, eventID, userID string) (int, bool, error) {
	return s.likes.State(ctx, eventID, userID)
}
