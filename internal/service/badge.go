package service

import (
	"context"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
)

// badgeStore is the badge-repository surface BadgeService needs.
type badgeStore interface {
	Award(ctx context.Context, userID, badgeType string, metadata map[string]any) (bool, error)
	ListByUser(ctx context.Context, userID string) ([]*domain.Badge, error)
}

// BadgeAwarder lets feature services award milestone badges without knowing the
// badge implementation (or even whether badges are wired). All hooks are
// optional and best-effort: a nil awarder or a failed award never fails the
// primary create/check-in action.
type BadgeAwarder interface {
	Award(ctx context.Context, userID, badgeType string, metadata map[string]any) error
}

type BadgeService struct {
	badges badgeStore
}

func NewBadgeService(badges badgeStore) *BadgeService {
	return &BadgeService{badges: badges}
}

// Award attempts an exactly-once milestone award. It never returns an error
// for the "already earned" or "milestone not met" cases — the awarder API is
// fire-and-forget. Only genuine storage failures surface.
func (s *BadgeService) Award(ctx context.Context, userID, badgeType string, metadata map[string]any) error {
	_, err := s.badges.Award(ctx, userID, badgeType, metadata)
	return err
}

// MyBadges returns the caller's earned badges, newest first.
func (s *BadgeService) MyBadges(ctx context.Context, userID string) ([]*domain.Badge, error) {
	return s.badges.ListByUser(ctx, userID)
}
