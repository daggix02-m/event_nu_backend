package service

import (
	"context"
	"net/http"
	"strings"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/daggix02-m/event_nu/backend/internal/validator"
)

// reportStore records the report row and applies the under_review moderation
// transition.
type reportStore interface {
	Create(ctx context.Context, rep *domain.Report) (*domain.Report, error)
}

type ReportService struct {
	reports  reportStore
	events   eventStore
	venues   venueStore
	comments commentStore
}

func NewReportService(reports reportStore, events eventStore, venues venueStore, comments commentStore) *ReportService {
	return &ReportService{reports: reports, events: events, venues: venues, comments: comments}
}

// Create files a moderation report against a target and flips it to
// 'under_review' (never auto-hidden). Targets that are publicly readable under
// RLS are existence-checked first; user targets cannot be validated because
// the users table is own-row scoped, so the report row is recorded as-is.
func (s *ReportService) Create(ctx context.Context, userID, entityType, entityID, reasonCode, description string) error {
	switch entityType {
	case "event":
		if _, err := s.events.GetByID(ctx, entityID); err != nil {
			return err
		}
	case "venue":
		if _, err := s.venues.GetByID(ctx, entityID); err != nil {
			return err
		}
	case "comment":
		if _, err := s.comments.GetByID(ctx, entityID); err != nil {
			return err
		}
	case "user":
		// users is own-row RLS: other users' rows are invisible, so existence
		// is not checked here (the report row itself is the record).
	default:
		return shared.NewAppError("invalid_target", "Unknown report target.", http.StatusUnprocessableEntity)
	}

	reasonCode = strings.TrimSpace(reasonCode)
	description = strings.TrimSpace(description)
	v := validator.New()
	v.Required(reasonCode, "reason_code")
	v.MaxChars(reasonCode, 100, "reason_code")
	v.MaxChars(description, 2000, "description")
	if !v.Valid() {
		return shared.NewAppError("validation_error", firstFieldError(v), http.StatusUnprocessableEntity)
	}

	_, err := s.reports.Create(ctx, &domain.Report{
		ReporterUserID: userID,
		EntityType:     entityType,
		EntityID:       entityID,
		ReasonCode:     reasonCode,
		Description:    description,
	})
	return err
}
