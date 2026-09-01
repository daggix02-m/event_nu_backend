package service

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/repository"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/daggix02-m/event_nu/backend/internal/validator"
)

type OrganizerService struct {
	orgs *repository.OrganizerRepository
}

func NewOrganizerService(orgs *repository.OrganizerRepository) *OrganizerService {
	return &OrganizerService{orgs: orgs}
}

// Apply creates an organizer application for a user. Only one pending
// application is allowed at a time.
func (s *OrganizerService) Apply(ctx context.Context, userID, requestedName, requestedSlug, bio string) (*domain.OrganizerApplication, error) {
	v := validator.New()
	v.Required(requestedName, "requested_name")
	v.MinChars(requestedName, 3, "requested_name")
	v.MaxChars(requestedName, 80, "requested_name")
	if requestedSlug != "" {
		v.MaxChars(requestedSlug, 60, "requested_slug")
	}
	v.MaxChars(bio, 2000, "bio")
	if !v.Valid() {
		return nil, shared.NewAppError("validation_error", firstFieldError(v), http.StatusUnprocessableEntity)
	}

	existing, err := s.orgs.GetApplicationByUser(ctx, userID)
	if err == nil && existing.Status == "pending" {
		return nil, shared.NewAppError("application_pending", "You already have a pending organizer application.", http.StatusConflict)
	}
	if err != nil && err != shared.ErrNotFound {
		return nil, err
	}

	app := &domain.OrganizerApplication{
		UserID:         userID,
		RequestedName:  strings.TrimSpace(requestedName),
		RequestedSlug:  slugify(firstNonEmpty(requestedSlug, requestedName)),
		Bio:            bio,
		Status:         "pending",
		SupportingData: map[string]any{},
	}
	if err := s.orgs.CreateApplication(ctx, app); err != nil {
		return nil, err
	}
	return app, nil
}

func (s *OrganizerService) GetMyApplication(ctx context.Context, userID string) (*domain.OrganizerApplication, error) {
	return s.orgs.GetApplicationByUser(ctx, userID)
}

// Approve reviews a pending application and creates the organizer record.
func (s *OrganizerService) Approve(ctx context.Context, applicationID, adminUserID, notes string) (*domain.Organizer, error) {
	app, err := s.orgs.GetApplicationByID(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	if app.Status != "pending" {
		return nil, shared.NewAppError("application_not_pending", "This application is not pending.", http.StatusConflict)
	}

	if err := s.orgs.ReviewApplication(ctx, applicationID, "approved", adminUserID, notes); err != nil {
		return nil, err
	}

	org, err := s.orgs.CreateOrganizer(ctx, &domain.Organizer{
		OwnerUserID: &app.UserID,
		Slug:        slugify(app.RequestedSlug),
		Name:        app.RequestedName,
		Bio:         app.Bio,
	})
	if err != nil {
		return nil, err
	}
	return org, nil
}

func (s *OrganizerService) Reject(ctx context.Context, applicationID, adminUserID, notes string) error {
	app, err := s.orgs.GetApplicationByID(ctx, applicationID)
	if err != nil {
		return err
	}
	if app.Status != "pending" {
		return shared.NewAppError("application_not_pending", "This application is not pending.", http.StatusConflict)
	}
	return s.orgs.ReviewApplication(ctx, applicationID, "rejected", adminUserID, notes)
}

var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugPattern.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}