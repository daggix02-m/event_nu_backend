package service

import (
	"context"
	"net/http"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/daggix02-m/event_nu/backend/internal/validator"
)

const reviewMaxChars = 5000

// reviewStore is the review-repository surface ReviewService needs.
type reviewStore interface {
	Create(ctx context.Context, rev *domain.Review) (*domain.Review, error)
	GetByID(ctx context.Context, id string) (*domain.Review, error)
	Update(ctx context.Context, id string, rating int16, body string) (*domain.Review, error)
	Delete(ctx context.Context, id string) error
	ListByEvent(ctx context.Context, eventID string, limit, offset int) ([]*domain.Review, error)
	CountByEvent(ctx context.Context, eventID string) (int, error)
}

type ReviewService struct {
	reviews reviewStore
	rsvps   rsvpStore
	events  eventStore
}

func NewReviewService(reviews reviewStore, rsvps rsvpStore, events eventStore) *ReviewService {
	return &ReviewService{reviews: reviews, rsvps: rsvps, events: events}
}

// Create requires that the caller actually attended: they must hold an active
// RSVP and the event must have started (the started-time check stands in for a
// QR check-in until Phase 14). One review per user per event → 409.
func (s *ReviewService) Create(ctx context.Context, userID, eventID string, rating int16, body string) (*domain.Review, error) {
	if err := s.validReview(rating, body); err != nil {
		return nil, err
	}
	event, err := s.events.GetByID(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if !event.StartsAt.Before(time.Now()) {
		return nil, shared.NewAppError("review_not_allowed", "You can review an event after it has started.", http.StatusForbidden)
	}
	rsvpd, err := s.rsvps.CountAttended(ctx, userID, eventID)
	if err != nil {
		return nil, err
	}
	if rsvpd == 0 {
		return nil, shared.NewAppError("review_not_allowed", "You can only review events you RSVP'd to.", http.StatusForbidden)
	}
	return s.reviews.Create(ctx, &domain.Review{EventID: eventID, UserID: userID, Rating: rating, Body: body})
}

func (s *ReviewService) Update(ctx context.Context, userID, reviewID string, rating int16, body string) (*domain.Review, error) {
	if err := s.validReview(rating, body); err != nil {
		return nil, err
	}
	existing, err := s.reviews.GetByID(ctx, reviewID)
	if err != nil {
		return nil, err
	}
	if existing.UserID != userID {
		return nil, shared.NewAppError("forbidden", "You can only edit your own reviews.", http.StatusForbidden)
	}
	return s.reviews.Update(ctx, reviewID, rating, body)
}

// Delete removes a review. Users delete their own; admins may remove any.
func (s *ReviewService) Delete(ctx context.Context, userID, reviewID string, admin bool) error {
	existing, err := s.reviews.GetByID(ctx, reviewID)
	if err != nil {
		return err
	}
	if existing.UserID != userID && !admin {
		return shared.NewAppError("forbidden", "You can only delete your own reviews.", http.StatusForbidden)
	}
	return s.reviews.Delete(ctx, reviewID)
}

// List returns published reviews for a visible event (paginated).
func (s *ReviewService) List(ctx context.Context, eventID string, page, limit int) (PageResult[*domain.Review], error) {
	if _, err := s.events.GetByID(ctx, eventID); err != nil {
		return PageResult[*domain.Review]{}, err
	}
	offset := (page - 1) * limit
	items, err := s.reviews.ListByEvent(ctx, eventID, limit, offset)
	if err != nil {
		return PageResult[*domain.Review]{}, err
	}
	total, err := s.reviews.CountByEvent(ctx, eventID)
	if err != nil {
		return PageResult[*domain.Review]{}, err
	}
	return PageResult[*domain.Review]{Items: items, Total: total}, nil
}

func (s *ReviewService) validReview(rating int16, body string) error {
	v := validator.New()
	v.Check(rating >= 1 && rating <= 5, "rating", "must be between 1 and 5")
	v.MaxChars(body, reviewMaxChars, "body")
	if !v.Valid() {
		return shared.NewAppError("validation_error", firstFieldError(v), http.StatusUnprocessableEntity)
	}
	return nil
}
