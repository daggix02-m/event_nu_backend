package service

import (
	"context"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

type stubPreferenceStore struct {
	getIDs     []string
	getAt      *time.Time
	replaceAt  time.Time
	activeCnt  int
	gotReplace []string
}

func (s *stubPreferenceStore) Get(_ context.Context, _ string) ([]string, *time.Time, error) {
	return s.getIDs, s.getAt, nil
}

func (s *stubPreferenceStore) Replace(_ context.Context, _ string, ids []string) (time.Time, error) {
	s.gotReplace = ids
	return s.replaceAt, nil
}

func (s *stubPreferenceStore) ActiveCategoryCount(_ context.Context, _ []string) (int, error) {
	return s.activeCnt, nil
}

type stubOnboardingUserStore struct {
	user           *domain.User
	completed      *domain.User
	completedCalls int
}

func (s *stubOnboardingUserStore) GetByID(_ context.Context, _ string) (*domain.User, error) {
	return s.user, nil
}

func (s *stubOnboardingUserStore) CompleteOnboarding(_ context.Context, _ string) (*domain.User, error) {
	s.completedCalls++
	if s.completed != nil {
		return s.completed, nil
	}
	return s.user, nil
}

func TestPreferencesReplaceCompletesOnboardingOnce(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	user := &domain.User{ID: "u1", OnboardingCompletedAt: nil}
	completed := &domain.User{ID: "u1", OnboardingCompletedAt: &now}

	prefs := &stubPreferenceStore{activeCnt: 2, replaceAt: now}
	users := &stubOnboardingUserStore{user: user, completed: completed}
	svc := NewPreferencesService(prefs, users)

	got, gotUser, err := svc.Replace(context.Background(), "u1", []string{" 1 ", "1", "", "2"})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if len(got.CategoryIDs) != 2 || got.CategoryIDs[0] != "1" || got.CategoryIDs[1] != "2" {
		t.Fatalf("expected deduped ordered ids [1 2], got %v", got.CategoryIDs)
	}
	if gotUser.OnboardingCompletedAt == nil {
		t.Fatal("replacing preferences must complete onboarding")
	}
	if users.completedCalls != 1 {
		t.Fatalf("expected exactly one CompleteOnboarding call, got %d", users.completedCalls)
	}

	// Second replace: user now has a stamp, so no extra CompleteOnboarding.
	user.OnboardingCompletedAt = &now
	if _, _, err := svc.Replace(context.Background(), "u1", []string{"1", "2"}); err != nil {
		t.Fatalf("replace after completion: %v", err)
	}
	if users.completedCalls != 1 {
		t.Fatalf("second replace must not re-stamp, got %d calls", users.completedCalls)
	}
}

func TestPreferencesReplaceRejectsEmpty(t *testing.T) {
	svc := NewPreferencesService(&stubPreferenceStore{}, &stubOnboardingUserStore{user: &domain.User{}})
	_, _, err := svc.Replace(context.Background(), "u1", nil)
	if !isAppErrorStatus(err, "invalid_categories", 400) {
		t.Fatalf("expected 400 invalid_categories for empty ids, got %v", err)
	}
}

func TestPreferencesReplaceRejectsUnknownCategory(t *testing.T) {
	svc := NewPreferencesService(&stubPreferenceStore{activeCnt: 1}, &stubOnboardingUserStore{user: &domain.User{}})
	_, _, err := svc.Replace(context.Background(), "u1", []string{"1", "2"})
	if !isAppErrorStatus(err, "invalid_categories", 400) {
		t.Fatalf("expected 400 invalid_categories for unknown id, got %v", err)
	}
}

func TestPreferencesSkipOnboarding(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	users := &stubOnboardingUserStore{
		user:      &domain.User{ID: "u1", OnboardingCompletedAt: &now},
		completed: &domain.User{ID: "u1", OnboardingCompletedAt: &now},
	}
	svc := NewPreferencesService(&stubPreferenceStore{}, users)
	u, err := svc.SkipOnboarding(context.Background(), "u1")
	if err != nil {
		t.Fatalf("skip: %v", err)
	}
	if u.OnboardingCompletedAt == nil {
		t.Fatal("skip must stamp onboarding_completed_at")
	}
	if users.completedCalls != 1 {
		t.Fatalf("expected one CompleteOnboarding call (idempotent at the DB), got %d", users.completedCalls)
	}
}

func isAppErrorStatus(err error, code string, status int) bool {
	appErr, ok := shared.AsAppError(err)
	return ok && appErr.Code == code && appErr.HTTPStatus == status
}
