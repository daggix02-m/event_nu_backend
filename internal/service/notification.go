package service

import (
	"context"
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// notifierStore is the notification-repository surface NotificationService
// depends on.
type notifierStore interface {
	Notify(ctx context.Context, userID, ntype, title, body string, data map[string]any) error
	ListByUser(ctx context.Context, userID string, limit, offset int) ([]*domain.Notification, error)
	CountByUser(ctx context.Context, userID string) (int, error)
	MarkRead(ctx context.Context, userID, id string) error
	MarkAllRead(ctx context.Context, userID string) (int64, error)
}

// Notifier lets domain services emit notifications without knowing the
// notification implementation. All hooks are optional: a nil Notifier (tests,
// direct repository use) is a no-op.
type Notifier interface {
	// NotifyEventOrganizer notifies the organizer's owner about activity on one
	// of their events.
	NotifyEventOrganizer(ctx context.Context, eventID, ntype, title, body string) error
	// NotifyUser notifies an arbitrary user (e.g. the organizer owner on a new
	// follow).
	NotifyUser(ctx context.Context, userID, ntype, title, body string) error
}

type NotificationService struct {
	notifications notifierStore
	events        eventStore
	orgs          organizerStore
}

func NewNotificationService(notifications notifierStore, events eventStore, orgs organizerStore) *NotificationService {
	return &NotificationService{notifications: notifications, events: events, orgs: orgs}
}

// NotifyEventOrganizer resolves the event's organizer owner and delivers a
// notification. Missing event/organizer/owner (or a request where the event is
// not visible under RLS) is silently skipped: the primary action already
// succeeded and notification fan-out must not fail it.
func (s *NotificationService) NotifyEventOrganizer(ctx context.Context, eventID, ntype, title, body string) error {
	ev, err := s.events.GetByID(ctx, eventID)
	if err != nil {
		return nil
	}
	org, err := s.orgs.GetOrganizerByID(ctx, ev.OrganizerID)
	if err != nil || org.OwnerUserID == nil {
		return nil
	}
	return s.notifications.Notify(ctx, *org.OwnerUserID, ntype, title, body, nil)
}

func (s *NotificationService) NotifyUser(ctx context.Context, userID, ntype, title, body string) error {
	if userID == "" {
		return nil
	}
	return s.notifications.Notify(ctx, userID, ntype, title, body, nil)
}

// List returns the caller's notification inbox, newest first.
func (s *NotificationService) List(ctx context.Context, userID string, page, limit int) (PageResult[*domain.Notification], error) {
	offset := (page - 1) * limit
	items, err := s.notifications.ListByUser(ctx, userID, limit, offset)
	if err != nil {
		return PageResult[*domain.Notification]{}, err
	}
	total, err := s.notifications.CountByUser(ctx, userID)
	if err != nil {
		return PageResult[*domain.Notification]{}, err
	}
	return PageResult[*domain.Notification]{Items: items, Total: total}, nil
}

func (s *NotificationService) Read(ctx context.Context, userID, id string) error {
	err := s.notifications.MarkRead(ctx, userID, id)
	if err == shared.ErrNotFound {
		return shared.NewAppError("notification_not_found", "No such notification.", http.StatusNotFound)
	}
	return err
}

func (s *NotificationService) ReadAll(ctx context.Context, userID string) (int64, error) {
	return s.notifications.MarkAllRead(ctx, userID)
}
