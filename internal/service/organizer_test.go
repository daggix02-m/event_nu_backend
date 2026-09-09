package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// stubOrganizerStore implements organizerStore with scripted methods so both
// OrganizerService and EventService.RequireOrganizer can be tested without a
// database. Unset methods default to "not found" / no-op behavior.
type stubOrganizerStore struct {
	onGetOrganizerByOwner  func(userID string) (*domain.Organizer, error)
	onGetOrganizerByID     func(id string) (*domain.Organizer, error)
	onGetApplicationByUser func(userID string) (*domain.OrganizerApplication, error)
	onCreateApplication    func(a *domain.OrganizerApplication) error
	onApproveApplication   func(id, adminUserID, notes string) (*domain.OrganizerApplication, error)
	onRejectApplication    func(id, adminUserID, notes string) error
	onCreateOrganizer      func(o *domain.Organizer) (*domain.Organizer, error)
}

func (s stubOrganizerStore) GetOrganizerByOwner(ctx context.Context, userID string) (*domain.Organizer, error) {
	if s.onGetOrganizerByOwner == nil {
		return nil, shared.ErrNotFound
	}
	return s.onGetOrganizerByOwner(userID)
}

func (s stubOrganizerStore) GetOrganizerByID(ctx context.Context, id string) (*domain.Organizer, error) {
	if s.onGetOrganizerByID == nil {
		return nil, shared.ErrNotFound
	}
	return s.onGetOrganizerByID(id)
}

func (s stubOrganizerStore) GetApplicationByUser(ctx context.Context, userID string) (*domain.OrganizerApplication, error) {
	if s.onGetApplicationByUser == nil {
		return nil, shared.ErrNotFound
	}
	return s.onGetApplicationByUser(userID)
}

func (s stubOrganizerStore) CreateApplication(ctx context.Context, a *domain.OrganizerApplication) error {
	if s.onCreateApplication == nil {
		return nil
	}
	return s.onCreateApplication(a)
}

func (s stubOrganizerStore) ApproveApplication(ctx context.Context, id, adminUserID, notes string) (*domain.OrganizerApplication, error) {
	if s.onApproveApplication == nil {
		return nil, nil
	}
	return s.onApproveApplication(id, adminUserID, notes)
}

func (s stubOrganizerStore) RejectApplication(ctx context.Context, id, adminUserID, notes string) error {
	if s.onRejectApplication == nil {
		return nil
	}
	return s.onRejectApplication(id, adminUserID, notes)
}

func (s stubOrganizerStore) CreateOrganizer(ctx context.Context, o *domain.Organizer) (*domain.Organizer, error) {
	if s.onCreateOrganizer == nil {
		return &domain.Organizer{ID: "org-created"}, nil
	}
	return s.onCreateOrganizer(o)
}

func activeOrganizer() *domain.Organizer {
	owner := "u-1"
	return &domain.Organizer{ID: "org-1", OwnerUserID: &owner, Slug: "acme", Name: "Acme", Bio: "", Status: "active"}
}

func pendingApplication() *domain.OrganizerApplication {
	return &domain.OrganizerApplication{ID: "app-1", UserID: "u-1", RequestedName: "Acme", RequestedSlug: "acme", Status: "pending"}
}

func assertAppError(t *testing.T, err error, code string, status int) {
	t.Helper()
	appErr, ok := shared.AsAppError(err)
	if !ok {
		t.Fatalf("expected *AppError, got %T (%v)", err, err)
	}
	if appErr.Code != code || appErr.HTTPStatus != status {
		t.Fatalf("expected %s/%d, got %s/%d", code, status, appErr.Code, appErr.HTTPStatus)
	}
}

func TestApplyValidation(t *testing.T) {
	tests := []struct {
		name    string
		nameArg string
		slugArg string
		bioArg  string
	}{
		{name: "empty name", nameArg: ""},
		{name: "short name", nameArg: "ab"},
		{name: "long name", nameArg: strings.Repeat("a", 81)},
		{name: "long slug", nameArg: "Acme", slugArg: strings.Repeat("s", 61)},
		{name: "long bio", nameArg: "Acme", bioArg: strings.Repeat("b", 2001)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewOrganizerService(stubOrganizerStore{})
			_, err := s.Apply(t.Context(), "u-1", tt.nameArg, tt.slugArg, tt.bioArg)
			assertAppError(t, err, "validation_error", http.StatusUnprocessableEntity)
		})
	}
}

func TestApplyRejectsWhilePending(t *testing.T) {
	var created bool
	s := NewOrganizerService(stubOrganizerStore{
		onGetApplicationByUser: func(userID string) (*domain.OrganizerApplication, error) {
			return pendingApplication(), nil
		},
		onCreateApplication: func(a *domain.OrganizerApplication) error {
			created = true
			return nil
		},
	})
	_, err := s.Apply(t.Context(), "u-1", "Acme", "", "bio")
	assertAppError(t, err, "application_pending", http.StatusConflict)
	if created {
		t.Fatal("must not create a second application while one is pending")
	}
}

func TestApplyAllowsAfterRejection(t *testing.T) {
	var created bool
	rejected := pendingApplication()
	rejected.Status = "rejected"
	s := NewOrganizerService(stubOrganizerStore{
		onGetApplicationByUser: func(userID string) (*domain.OrganizerApplication, error) {
			return rejected, nil
		},
		onCreateApplication: func(a *domain.OrganizerApplication) error {
			created = true
			return nil
		},
	})
	app, err := s.Apply(t.Context(), "u-1", "Acme", "", "bio")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Fatal("expected a new application to be created")
	}
	if app.Status != "pending" {
		t.Fatalf("expected pending application, got %q", app.Status)
	}
}

func TestApplyPropagatesStoreError(t *testing.T) {
	boom := errors.New("store down")
	s := NewOrganizerService(stubOrganizerStore{
		onGetApplicationByUser: func(userID string) (*domain.OrganizerApplication, error) {
			return nil, boom
		},
	})
	if _, err := s.Apply(t.Context(), "u-1", "Acme", "", "bio"); !errors.Is(err, boom) {
		t.Fatalf("expected store error to propagate, got %v", err)
	}
}

func TestApplyCreatesSlugFromNameWhenBlank(t *testing.T) {
	var got *domain.OrganizerApplication
	s := NewOrganizerService(stubOrganizerStore{
		onCreateApplication: func(a *domain.OrganizerApplication) error {
			got = a
			return nil
		},
	})
	app, err := s.Apply(t.Context(), "u-1", "  My Great Org  ", "", "bio")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.RequestedSlug != "my-great-org" {
		t.Fatalf("expected slugified fallback slug, got %q", got.RequestedSlug)
	}
	if got.RequestedName != "My Great Org" {
		t.Fatalf("expected trimmed name, got %q", got.RequestedName)
	}
	if got.SupportingData == nil || len(got.SupportingData) != 0 {
		t.Fatalf("expected empty SupportingData, got %v", got.SupportingData)
	}
	if app != got {
		t.Fatal("expected the created application to be returned")
	}
}

func TestApplyPropagatesCreateError(t *testing.T) {
	boom := errors.New("insert failed")
	s := NewOrganizerService(stubOrganizerStore{
		onCreateApplication: func(a *domain.OrganizerApplication) error { return boom },
	})
	if _, err := s.Apply(t.Context(), "u-1", "Acme", "", "bio"); !errors.Is(err, boom) {
		t.Fatalf("expected create error to propagate, got %v", err)
	}
}

func TestGetMyApplication(t *testing.T) {
	app := pendingApplication()
	s := NewOrganizerService(stubOrganizerStore{
		onGetApplicationByUser: func(userID string) (*domain.OrganizerApplication, error) {
			if userID != "u-1" {
				t.Fatalf("expected lookup by u-1, got %q", userID)
			}
			return app, nil
		},
	})
	got, err := s.GetMyApplication(t.Context(), "u-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != app {
		t.Fatalf("expected stubbed application back, got %v", got)
	}
}

func TestGetMyApplicationPropagatesNotFound(t *testing.T) {
	s := NewOrganizerService(stubOrganizerStore{})
	if _, err := s.GetMyApplication(t.Context(), "u-1"); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestApproveSuccessCreatesOrganizerFromApplication(t *testing.T) {
	app := pendingApplication()
	var approveID, admin, notes string
	var createdOrg *domain.Organizer
	s := NewOrganizerService(stubOrganizerStore{
		onApproveApplication: func(id, adminUserID, n string) (*domain.OrganizerApplication, error) {
			approveID, admin, notes = id, adminUserID, n
			return app, nil
		},
		onCreateOrganizer: func(o *domain.Organizer) (*domain.Organizer, error) {
			createdOrg = o
			return &domain.Organizer{ID: "org-2", OwnerUserID: o.OwnerUserID, Slug: o.Slug, Name: o.Name, Bio: o.Bio, Status: "active"}, nil
		},
	})
	org, err := s.Approve(t.Context(), "app-1", "admin-1", "looks good")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if approveID != "app-1" || admin != "admin-1" || notes != "looks good" {
		t.Fatalf("unexpected approve args: id=%q admin=%q notes=%q", approveID, admin, notes)
	}
	if createdOrg == nil {
		t.Fatal("organizer must be created")
	}
	if createdOrg.Slug != "acme" || createdOrg.Name != "Acme" {
		t.Fatalf("organizer must be built from the application, got %+v", createdOrg)
	}
	if org.ID != "org-2" {
		t.Fatalf("expected created organizer to be returned, got %+v", org)
	}
}

func TestApproveConflictMapsTo409(t *testing.T) {
	s := NewOrganizerService(stubOrganizerStore{
		onApproveApplication: func(id, adminUserID, notes string) (*domain.OrganizerApplication, error) {
			return nil, shared.ErrConflict
		},
	})
	_, err := s.Approve(t.Context(), "app-1", "admin-1", "")
	assertAppError(t, err, "application_not_pending", http.StatusConflict)
}

func TestApprovePropagatesErrors(t *testing.T) {
	boom := errors.New("store down")
	s := NewOrganizerService(stubOrganizerStore{
		onApproveApplication: func(id, adminUserID, notes string) (*domain.OrganizerApplication, error) {
			return nil, boom
		},
	})
	if _, err := s.Approve(t.Context(), "app-1", "admin-1", ""); !errors.Is(err, boom) {
		t.Fatalf("expected store error to propagate, got %v", err)
	}
}

func TestApprovePropagatesOrganizerCreateError(t *testing.T) {
	boom := errors.New("insert organizer failed")
	s := NewOrganizerService(stubOrganizerStore{
		onApproveApplication: func(id, adminUserID, notes string) (*domain.OrganizerApplication, error) {
			return pendingApplication(), nil
		},
		onCreateOrganizer: func(o *domain.Organizer) (*domain.Organizer, error) { return nil, boom },
	})
	if _, err := s.Approve(t.Context(), "app-1", "admin-1", ""); !errors.Is(err, boom) {
		t.Fatalf("expected organizer create error to propagate, got %v", err)
	}
}

func TestRejectSuccess(t *testing.T) {
	var rejectID, admin, notes string
	s := NewOrganizerService(stubOrganizerStore{
		onRejectApplication: func(id, adminUserID, n string) error {
			rejectID, admin, notes = id, adminUserID, n
			return nil
		},
	})
	if err := s.Reject(t.Context(), "app-1", "admin-1", "spam"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rejectID != "app-1" || admin != "admin-1" || notes != "spam" {
		t.Fatalf("unexpected reject args: id=%q admin=%q notes=%q", rejectID, admin, notes)
	}
}

func TestRejectConflictMapsTo409(t *testing.T) {
	s := NewOrganizerService(stubOrganizerStore{
		onRejectApplication: func(id, adminUserID, notes string) error { return shared.ErrConflict },
	})
	err := s.Reject(t.Context(), "app-1", "admin-1", "")
	assertAppError(t, err, "application_not_pending", http.StatusConflict)
}

func TestRejectPropagatesError(t *testing.T) {
	boom := errors.New("store down")
	s := NewOrganizerService(stubOrganizerStore{
		onRejectApplication: func(id, adminUserID, notes string) error { return boom },
	})
	if err := s.Reject(t.Context(), "app-1", "admin-1", ""); !errors.Is(err, boom) {
		t.Fatalf("expected store error to propagate, got %v", err)
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "My Venue Name", want: "my-venue-name"},
		{in: "  ACME & Sons, Inc.  ", want: "acme-sons-inc"},
		{in: "already-slug", want: "already-slug"},
		{in: "UPPER_case.name", want: "upper-case-name"},
		{in: "!!!日本語!!!", want: ""},
		{in: "", want: ""},
		{in: "---trim---", want: "trim"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := slugify(tt.in); got != tt.want {
				t.Fatalf("slugify(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty(" x ", "fallback"); got != " x " {
		t.Fatalf("expected first non-empty value untouched, got %q", got)
	}
	if got := firstNonEmpty("   ", "fallback"); got != "fallback" {
		t.Fatalf("expected whitespace to fall through, got %q", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Fatalf("expected empty result for two empties, got %q", got)
	}
}
