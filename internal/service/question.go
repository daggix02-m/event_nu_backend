package service

import (
	"context"
	"net/http"
	"strings"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/daggix02-m/event_nu/backend/internal/validator"
)

type questionStore interface {
	Create(ctx context.Context, q *domain.Question) (*domain.Question, error)
	GetByID(ctx context.Context, id string) (*domain.Question, error)
	SetPinned(ctx context.Context, id string, pinned bool) error
	SetAnswer(ctx context.Context, id string, answer *string) error
	Delete(ctx context.Context, id string) error
	ListByEvent(ctx context.Context, eventID, viewerID string, limit, offset int) ([]*domain.QuestionWithVote, error)
	CountByEvent(ctx context.Context, eventID string) (int, error)
}

type voteStore interface {
	AddVote(ctx context.Context, questionID, userID string) error
	RemoveVote(ctx context.Context, questionID, userID string) error
}

type QuestionService struct {
	questions questionStore
	votes     voteStore
	events    eventStore
	orgs      organizerStore
}

func NewQuestionService(questions questionStore, votes voteStore, events eventStore, orgs organizerStore) *QuestionService {
	return &QuestionService{questions: questions, votes: votes, events: events, orgs: orgs}
}

func (s *QuestionService) requireEventOrganizer(ctx context.Context, userID, eventID string) error {
	org, err := s.orgs.GetOrganizerByOwner(ctx, userID)
	if err != nil {
		if err == shared.ErrNotFound {
			return shared.NewAppError("organizer_required", "You need an approved organizer account.", http.StatusForbidden)
		}
		return err
	}
	if org.Status != "active" {
		return shared.NewAppError("organizer_required", "Your organizer account is not active.", http.StatusForbidden)
	}
	ev, err := s.events.GetByID(ctx, eventID)
	if err != nil {
		return err
	}
	if ev.OrganizerID != org.ID {
		return shared.NewAppError("forbidden", "You can only manage your own events.", http.StatusForbidden)
	}
	return nil
}

func (s *QuestionService) Create(ctx context.Context, userID, eventID, body string) (*domain.Question, error) {
	if _, err := s.events.GetByID(ctx, eventID); err != nil {
		return nil, err
	}
	v := validator.New()
	v.Required(body, "body")
	v.MaxChars(body, 2000, "body")
	if !v.Valid() {
		return nil, shared.NewAppError("validation_error", firstFieldError(v), http.StatusUnprocessableEntity)
	}
	return s.questions.Create(ctx, &domain.Question{
		EventID: eventID,
		UserID:  userID,
		Body:    strings.TrimSpace(body),
	})
}

func (s *QuestionService) List(ctx context.Context, eventID, viewerID string, page, limit int) (PageResult[*domain.QuestionWithVote], error) {
	if _, err := s.events.GetByID(ctx, eventID); err != nil {
		return PageResult[*domain.QuestionWithVote]{}, err
	}
	offset := (page - 1) * limit
	items, err := s.questions.ListByEvent(ctx, eventID, viewerID, limit, offset)
	if err != nil {
		return PageResult[*domain.QuestionWithVote]{}, err
	}
	total, err := s.questions.CountByEvent(ctx, eventID)
	if err != nil {
		return PageResult[*domain.QuestionWithVote]{}, err
	}
	return PageResult[*domain.QuestionWithVote]{Items: items, Total: total}, nil
}

func (s *QuestionService) Upvote(ctx context.Context, userID, questionID string) error {
	if _, err := s.questions.GetByID(ctx, questionID); err != nil {
		return err
	}
	return s.votes.AddVote(ctx, questionID, userID)
}

func (s *QuestionService) RemoveUpvote(ctx context.Context, userID, questionID string) error {
	if _, err := s.questions.GetByID(ctx, questionID); err != nil {
		return err
	}
	return s.votes.RemoveVote(ctx, questionID, userID)
}

func (s *QuestionService) Pin(ctx context.Context, userID, questionID string, pinned bool) error {
	q, err := s.questions.GetByID(ctx, questionID)
	if err != nil {
		return err
	}
	if err := s.requireEventOrganizer(ctx, userID, q.EventID); err != nil {
		return err
	}
	return s.questions.SetPinned(ctx, questionID, pinned)
}

func (s *QuestionService) Answer(ctx context.Context, userID, questionID, answer string) error {
	q, err := s.questions.GetByID(ctx, questionID)
	if err != nil {
		return err
	}
	if err := s.requireEventOrganizer(ctx, userID, q.EventID); err != nil {
		return err
	}
	var ans *string
	if strings.TrimSpace(answer) != "" {
		t := strings.TrimSpace(answer)
		ans = &t
	}
	return s.questions.SetAnswer(ctx, questionID, ans)
}

func (s *QuestionService) Delete(ctx context.Context, userID, questionID string, admin bool) error {
	q, err := s.questions.GetByID(ctx, questionID)
	if err != nil {
		return err
	}
	if q.UserID == userID {
		return s.questions.Delete(ctx, questionID)
	}
	if admin {
		return s.questions.Delete(ctx, questionID)
	}
	return shared.NewAppError("forbidden", "You can only delete your own questions.", http.StatusForbidden)
}
