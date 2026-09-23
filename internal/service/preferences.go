package service

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// preferenceStore is the category-preference repository surface the service
// depends on. Queries run under the request's RLS context, so per-user rows are
// isolated by the database itself.
type preferenceStore interface {
	Get(ctx context.Context, userID string) ([]string, *time.Time, error)
	Replace(ctx context.Context, userID string, categoryIDs []string) (time.Time, error)
	ActiveCategoryCount(ctx context.Context, categoryIDs []string) (int, error)
}

// onboardingUserStore is the user surface the preferences service needs to
// stamp onboarding completion. Kept intentionally narrow for unit testing.
type onboardingUserStore interface {
	GetByID(ctx context.Context, id string) (*domain.User, error)
	CompleteOnboarding(ctx context.Context, id string) (*domain.User, error)
}

// Preferences is the category-preference state returned to the client.
type Preferences struct {
	CategoryIDs []string
	UpdatedAt   *time.Time
}

// PreferencesService owns the interactive onboarding + interests flow: reading
// and replacing a user's category preferences, and stamping onboarding as
// complete. Replacing preferences with at least one valid, active category
// implicitly completes onboarding (a user who picks interests never gets
// re-prompted); SkipOnboarding does the same without touching preferences.
type PreferencesService struct {
	prefs preferenceStore
	users onboardingUserStore
}

func NewPreferencesService(prefs preferenceStore, users onboardingUserStore) *PreferencesService {
	return &PreferencesService{prefs: prefs, users: users}
}

// Get returns the user's preferences plus their current profile (for the
// onboarding_completed flag).
func (s *PreferencesService) Get(ctx context.Context, userID string) (Preferences, *domain.User, error) {
	ids, updatedAt, err := s.prefs.Get(ctx, userID)
	if err != nil {
		return Preferences{}, nil, err
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return Preferences{}, nil, err
	}
	return Preferences{CategoryIDs: ids, UpdatedAt: updatedAt}, user, nil
}

// Replace overwrites the user's category preferences. Input is normalized
// (trimmed + deduplicated, order preserved) and must contain at least one id
// that is a currently active category. When the user has not completed
// onboarding yet, doing so here stamps it — picking your interests IS finishing
// onboarding.
func (s *PreferencesService) Replace(ctx context.Context, userID string, categoryIDs []string) (Preferences, *domain.User, error) {
	ids := normalizeCategoryIDs(categoryIDs)
	if len(ids) == 0 {
		return Preferences{}, nil, shared.NewAppError("invalid_categories", "At least one category is required.", http.StatusBadRequest)
	}

	active, err := s.prefs.ActiveCategoryCount(ctx, ids)
	if err != nil {
		return Preferences{}, nil, err
	}
	if active != len(ids) {
		return Preferences{}, nil, shared.NewAppError("invalid_categories", "One or more categories are unknown or inactive.", http.StatusBadRequest)
	}

	updatedAt, err := s.prefs.Replace(ctx, userID, ids)
	if err != nil {
		return Preferences{}, nil, err
	}

	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return Preferences{}, nil, err
	}
	if user.OnboardingCompletedAt == nil {
		user, err = s.users.CompleteOnboarding(ctx, userID)
		if err != nil {
			return Preferences{}, nil, err
		}
	}

	return Preferences{CategoryIDs: ids, UpdatedAt: &updatedAt}, user, nil
}

// SkipOnboarding stamps onboarding as completed without recording any
// preferences. Idempotent: the migration keeps the original timestamp on a
// second call.
func (s *PreferencesService) SkipOnboarding(ctx context.Context, userID string) (*domain.User, error) {
	return s.users.CompleteOnboarding(ctx, userID)
}

// normalizeCategoryIDs trims whitespace and removes duplicates, preserving the
// input order.
func normalizeCategoryIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
