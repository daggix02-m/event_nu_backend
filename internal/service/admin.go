package service

import (
	"context"
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// reportAdminStore is the moderation-report surface AdminService needs for its
// review queue. Queries run under the admin RLS role (is_privileged), so every
// row is visible.
type reportAdminStore interface {
	ListAll(ctx context.Context, limit, offset int) ([]*domain.Report, error)
	CountAll(ctx context.Context) (int, error)
	Resolve(ctx context.Context, id, resolution string) (*domain.Report, error)
}

// applicationAdminStore is the organizer-application surface the admin queue
// reads. Status is an optional filter; queries run under the admin RLS role.
type applicationAdminStore interface {
	ListApplications(ctx context.Context, status string, limit, offset int) ([]*domain.OrganizerApplication, error)
	CountApplications(ctx context.Context, status string) (int, error)
}

// adminEventsStore is the event surface the moderation list reads. Unlike the
// shared eventStore it intentionally has no public "published, not blocked"
// restriction — the admin queue sees drafts and blocked events.
type adminEventsStore interface {
	AdminList(ctx context.Context, status, moderation string, limit, offset int) ([]*domain.Event, error)
	AdminCount(ctx context.Context, status, moderation string) (int, error)
}

// applicationStatuses mirrors organizer_application_status (migration 00007).
var applicationStatuses = map[string]bool{
	"pending": true, "approved": true, "rejected": true,
	"needs_more_information": true, "withdrawn": true,
}

// eventStatuses mirrors event_status (00007); moderationStatuses mirrors
// moderation_status (00013).
var eventStatuses = map[string]bool{
	"draft": true, "published": true, "cancelled": true, "completed": true, "archived": true,
}

var moderationStatuses = map[string]bool{
	"clean": true, "reported": true, "under_review": true, "blocked": true, "restored": true,
}

// AdminService is the moderation surface: the report review queue and explicit
// event moderation transitions. Event blocking/restoring touches ONLY
// moderation_status — the event lifecycle status (draft/published/cancelled)
// is deliberately left untouched, so a blocked event stays 'published' but is
// hidden from public listing/detail while blocked.
type AdminService struct {
	reports     reportAdminStore
	events      eventStore
	apps        applicationAdminStore
	adminEvents adminEventsStore
}

func NewAdminService(reports reportAdminStore, events eventStore, apps applicationAdminStore, adminEvents adminEventsStore) *AdminService {
	return &AdminService{reports: reports, events: events, apps: apps, adminEvents: adminEvents}
}

func (s *AdminService) ListReports(ctx context.Context, page, limit int) (PageResult[*domain.Report], error) {
	offset := (page - 1) * limit
	items, err := s.reports.ListAll(ctx, limit, offset)
	if err != nil {
		return PageResult[*domain.Report]{}, err
	}
	total, err := s.reports.CountAll(ctx)
	if err != nil {
		return PageResult[*domain.Report]{}, err
	}
	return PageResult[*domain.Report]{Items: items, Total: total}, nil
}

// ListApplications returns the organizer-application queue, pending first.
// The status filter must be a real application state; anything else is a
// validation error rather than a silently-empty result.
func (s *AdminService) ListApplications(ctx context.Context, page, limit int, status string) (PageResult[*domain.OrganizerApplication], error) {
	if status != "" && !applicationStatuses[status] {
		return PageResult[*domain.OrganizerApplication]{},
			shared.NewAppError("validation_error", "status must be one of pending, approved, rejected, needs_more_information, withdrawn.", http.StatusUnprocessableEntity)
	}
	offset := (page - 1) * limit
	items, err := s.apps.ListApplications(ctx, status, limit, offset)
	if err != nil {
		return PageResult[*domain.OrganizerApplication]{}, err
	}
	total, err := s.apps.CountApplications(ctx, status)
	if err != nil {
		return PageResult[*domain.OrganizerApplication]{}, err
	}
	return PageResult[*domain.OrganizerApplication]{Items: items, Total: total}, nil
}

// adminEventFilterError reports an unknown filter value for admin lists.
func adminEventFilterError(value string) error {
	return shared.NewAppError("validation_error", value+" is not a valid filter value.", http.StatusUnprocessableEntity)
}

// ListEvents returns every event (draft, published, blocked, ...) matching the
// optional lifecycle/moderation filters, newest first. Unknown filter values
// are validation errors rather than silently-empty results.
func (s *AdminService) ListEvents(ctx context.Context, page, limit int, status, moderation string) (PageResult[*domain.Event], error) {
	if status != "" && !eventStatuses[status] {
		return PageResult[*domain.Event]{}, adminEventFilterError("status")
	}
	if moderation != "" && !moderationStatuses[moderation] {
		return PageResult[*domain.Event]{}, adminEventFilterError("moderation_status")
	}
	offset := (page - 1) * limit
	items, err := s.adminEvents.AdminList(ctx, status, moderation, limit, offset)
	if err != nil {
		return PageResult[*domain.Event]{}, err
	}
	total, err := s.adminEvents.AdminCount(ctx, status, moderation)
	if err != nil {
		return PageResult[*domain.Event]{}, err
	}
	return PageResult[*domain.Event]{Items: items, Total: total}, nil
}

// ResolveReport closes a report. Resolution is optional and length-limited.
func (s *AdminService) ResolveReport(ctx context.Context, id, resolution string) (*domain.Report, error) {
	if len(resolution) > 2000 {
		return nil, shared.NewAppError("validation_error", "resolution must be at most 2000 characters.", http.StatusUnprocessableEntity)
	}
	report, err := s.reports.Resolve(ctx, id, resolution)
	if err == shared.ErrNotFound {
		return nil, shared.NewAppError("report_not_found", "No such open report.", http.StatusNotFound)
	}
	return report, err
}

// BlockEvent hides an event from all public surfaces; the lifecycle status is
// untouched.
func (s *AdminService) BlockEvent(ctx context.Context, eventID string) (*domain.Event, error) {
	if err := s.events.SetModeration(ctx, eventID, "blocked"); err != nil {
		return nil, err
	}
	return s.events.GetByID(ctx, eventID)
}

// RestoreEvent clears an event's moderation block.
func (s *AdminService) RestoreEvent(ctx context.Context, eventID string) (*domain.Event, error) {
	if err := s.events.SetModeration(ctx, eventID, "clean"); err != nil {
		return nil, err
	}
	return s.events.GetByID(ctx, eventID)
}
