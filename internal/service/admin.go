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

// AdminService is the moderation surface: the report review queue and explicit
// event moderation transitions. Event blocking/restoring touches ONLY
// moderation_status — the event lifecycle status (draft/published/cancelled)
// is deliberately left untouched, so a blocked event stays 'published' but is
// hidden from public listing/detail while blocked.
type AdminService struct {
	reports reportAdminStore
	events  eventStore
}

func NewAdminService(reports reportAdminStore, events eventStore) *AdminService {
	return &AdminService{reports: reports, events: events}
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
