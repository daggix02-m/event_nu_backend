package service

import (
	"context"
	"net/http"
	"strings"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/daggix02-m/event_nu/backend/internal/validator"
)

const commentMaxChars = 2000

// commentStore is the comment-repository surface CommentService depends on.
type commentStore interface {
	Create(ctx context.Context, c *domain.Comment) (*domain.Comment, error)
	GetByID(ctx context.Context, id string) (*domain.Comment, error)
	UpdateBody(ctx context.Context, id, body string) (*domain.Comment, error)
	Delete(ctx context.Context, id string) error
	ListByEvent(ctx context.Context, eventID string, limit, offset int) ([]*domain.Comment, error)
	CountByEvent(ctx context.Context, eventID string) (int, error)
}

type CommentService struct {
	comments commentStore
	events   eventStore
	notifier Notifier
}

func NewCommentService(comments commentStore, events eventStore) *CommentService {
	return &CommentService{comments: comments, events: events}
}

// SetNotifier optionally attaches the notification hook (nil-safe). Wired only
// once at application startup; kept separate from the constructor so tests
// don't need to change when notifications are added.
func (s *CommentService) SetNotifier(n Notifier) {
	s.notifier = n
}

// eventVisible ensures the target event is visible under current RLS (i.e. a
// published, non-blocked event) before any comment is attached to it.
func (s *CommentService) eventVisible(ctx context.Context, eventID string) error {
	_, err := s.events.GetByID(ctx, eventID)
	return err
}

func (s *CommentService) Create(ctx context.Context, userID, eventID, body string) (*domain.Comment, error) {
	body = strings.TrimSpace(body)
	if err := s.validBody(body); err != nil {
		return nil, err
	}
	if err := s.eventVisible(ctx, eventID); err != nil {
		return nil, err
	}
	comment, err := s.comments.Create(ctx, &domain.Comment{EventID: eventID, UserID: userID, Body: body})
	if err != nil {
		return nil, err
	}
	if s.notifier != nil {
		_ = s.notifier.NotifyEventOrganizer(ctx, eventID, "comment", "New comment on your event", body)
	}
	return comment, nil
}

func (s *CommentService) Update(ctx context.Context, userID, commentID, body string) (*domain.Comment, error) {
	body = strings.TrimSpace(body)
	if err := s.validBody(body); err != nil {
		return nil, err
	}
	existing, err := s.comments.GetByID(ctx, commentID)
	if err != nil {
		return nil, err
	}
	if existing.UserID != userID {
		return nil, shared.NewAppError("forbidden", "You can only edit your own comments.", http.StatusForbidden)
	}
	return s.comments.UpdateBody(ctx, commentID, body)
}

// Delete removes a comment. Users can delete their own; admins may remove any.
// The repo soft-deletes via UPDATE, which RLS permits for the owner (or a
// privileged role when admin is true).
func (s *CommentService) Delete(ctx context.Context, userID, commentID string, admin bool) error {
	existing, err := s.comments.GetByID(ctx, commentID)
	if err != nil {
		return err
	}
	if existing.UserID != userID && !admin {
		return shared.NewAppError("forbidden", "You can only delete your own comments.", http.StatusForbidden)
	}
	return s.comments.Delete(ctx, commentID)
}

func (s *CommentService) List(ctx context.Context, eventID string, page, limit int) (PageResult[*domain.Comment], error) {
	if err := s.eventVisible(ctx, eventID); err != nil {
		return PageResult[*domain.Comment]{}, err
	}
	offset := (page - 1) * limit
	items, err := s.comments.ListByEvent(ctx, eventID, limit, offset)
	if err != nil {
		return PageResult[*domain.Comment]{}, err
	}
	total, err := s.comments.CountByEvent(ctx, eventID)
	if err != nil {
		return PageResult[*domain.Comment]{}, err
	}
	return PageResult[*domain.Comment]{Items: items, Total: total}, nil
}

func (s *CommentService) validBody(body string) error {
	v := validator.New()
	v.Required(body, "body")
	v.MaxChars(body, commentMaxChars, "body")
	if !v.Valid() {
		return shared.NewAppError("validation_error", firstFieldError(v), http.StatusUnprocessableEntity)
	}
	return nil
}
