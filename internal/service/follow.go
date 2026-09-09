package service

import (
	"context"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// followStore is the follow-repository surface FollowService needs.
type followStore interface {
	Follow(ctx context.Context, userID, organizerID string) (bool, error)
	Unfollow(ctx context.Context, userID, organizerID string) error
	State(ctx context.Context, organizerID, userID string) (int, bool, error)
}

type FollowService struct {
	follows  followStore
	orgs     organizerStore
	notifier Notifier
}

func NewFollowService(follows followStore, orgs organizerStore) *FollowService {
	return &FollowService{follows: follows, orgs: orgs}
}

// SetNotifier optionally attaches the notification hook (nil-safe).
func (s *FollowService) SetNotifier(n Notifier) {
	s.notifier = n
}

// requireVisibleOrganizer resolves an organizer that is publicly visible (i.e.
// active and not archived under the organizers_read_public policy).
func (s *FollowService) requireVisibleOrganizer(ctx context.Context, organizerID string) (*domain.Organizer, error) {
	org, err := s.orgs.GetOrganizerByID(ctx, organizerID)
	if err != nil {
		if err == shared.ErrNotFound {
			return nil, shared.ErrNotFound
		}
		return nil, err
	}
	if org.Status != "active" {
		return nil, shared.ErrNotFound
	}
	return org, nil
}

// Follow is idempotent; the upsert makes re-following a no-op. The organizer
// owner gets a 'follow' notification on a new (i.e. confirmed-insert) follow.
func (s *FollowService) Follow(ctx context.Context, userID, organizerID string) (bool, error) {
	org, err := s.requireVisibleOrganizer(ctx, organizerID)
	if err != nil {
		return false, err
	}
	created, err := s.follows.Follow(ctx, userID, organizerID)
	if err != nil {
		return false, err
	}
	if created && s.notifier != nil && org.OwnerUserID != nil {
		_ = s.notifier.NotifyUser(ctx, *org.OwnerUserID, "follow", "New follower", org.Name)
	}
	return created, nil
}

// Unfollow is idempotent and works even if the organizer is later suspended.
func (s *FollowService) Unfollow(ctx context.Context, userID, organizerID string) error {
	return s.follows.Unfollow(ctx, userID, organizerID)
}

// State returns the follower count and whether the user (empty = anonymous)
// follows the organizer.
func (s *FollowService) State(ctx context.Context, organizerID, userID string) (int, bool, error) {
	return s.follows.State(ctx, organizerID, userID)
}

// GetOrganizer resolves the publicly visible organizer profile.
func (s *FollowService) GetOrganizer(ctx context.Context, organizerID string) (*domain.Organizer, error) {
	return s.requireVisibleOrganizer(ctx, organizerID)
}

// Profile resolves the organizer (publicly visible) with follow aggregates.
func (s *FollowService) Profile(ctx context.Context, organizerID, userID string) (*domain.Organizer, int, bool, error) {
	org, err := s.requireVisibleOrganizer(ctx, organizerID)
	if err != nil {
		return nil, 0, false, err
	}
	count, followed, err := s.follows.State(ctx, organizerID, userID)
	if err != nil {
		return nil, 0, false, err
	}
	return org, count, followed, nil
}
