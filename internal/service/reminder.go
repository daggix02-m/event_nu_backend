package service

import (
	"context"
	"net/http"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// reminderStore is the reminder-repository surface ReminderService needs.
type reminderStore interface {
	Create(ctx context.Context, rem *domain.Reminder) (*domain.Reminder, error)
	Delete(ctx context.Context, userID, eventID string) error
	ListByUser(ctx context.Context, userID string, limit, offset int) ([]*domain.ReminderWithEvent, error)
	CountByUser(ctx context.Context, userID string) (int, error)
}

type ReminderService struct {
	reminders reminderStore
	events    eventStore
}

func NewReminderService(reminders reminderStore, events eventStore) *ReminderService {
	return &ReminderService{reminders: reminders, events: events}
}

// Create sets a reminder ahead of a published event. The reminder boundary is
// the event start; remind_at must be in the future and before starts_at.
func (s *ReminderService) Create(ctx context.Context, userID, eventID string, remindAt time.Time) (*domain.Reminder, error) {
	ev, err := s.events.GetByID(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if ev.StartsAt.Before(time.Now()) {
		return nil, shared.NewAppError("event_started", "This event has already started.", http.StatusConflict)
	}
	if remindAt.Before(time.Now()) || remindAt.After(ev.StartsAt) {
		return nil, shared.NewAppError("validation_error", "remind_at must be in the future and before the event start.", http.StatusUnprocessableEntity)
	}
	return s.reminders.Create(ctx, &domain.Reminder{EventID: eventID, UserID: userID, RemindAt: remindAt})
}

// Delete removes the caller's reminder for an event; idempotent.
func (s *ReminderService) Delete(ctx context.Context, userID, eventID string) error {
	return s.reminders.Delete(ctx, userID, eventID)
}

func (s *ReminderService) MyReminders(ctx context.Context, userID string, page, limit int) (PageResult[*domain.ReminderWithEvent], error) {
	offset := (page - 1) * limit
	items, err := s.reminders.ListByUser(ctx, userID, limit, offset)
	if err != nil {
		return PageResult[*domain.ReminderWithEvent]{}, err
	}
	total, err := s.reminders.CountByUser(ctx, userID)
	if err != nil {
		return PageResult[*domain.ReminderWithEvent]{}, err
	}
	return PageResult[*domain.ReminderWithEvent]{Items: items, Total: total}, nil
}
