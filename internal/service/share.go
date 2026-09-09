package service

import (
	"context"
	"net/http"
	"strings"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

const (
	shareMaxChannel = 32
	shareMaxRef     = 200
)

// shareStore is the share-repository surface ShareService needs.
type shareStore interface {
	Record(ctx context.Context, s *domain.Share) error
}

type ShareService struct {
	shares shareStore
	events eventStore
}

func NewShareService(shares shareStore, events eventStore) *ShareService {
	return &ShareService{shares: shares, events: events}
}

// RecordShare records a share-analytics row for a publicly visible event.
// Repeat shares of the same (event, channel, ref) are deduplicated.
func (s *ShareService) RecordShare(ctx context.Context, userID, eventID, channel, ref string) error {
	if _, err := s.events.GetByID(ctx, eventID); err != nil {
		return err
	}
	channel = strings.TrimSpace(channel)
	ref = strings.TrimSpace(ref)
	if channel == "" {
		return shared.NewAppError("validation_error", "channel is required.", http.StatusUnprocessableEntity)
	}
	if len(channel) > shareMaxChannel {
		return shared.NewAppError("validation_error", "channel must be 32 characters or fewer.", http.StatusUnprocessableEntity)
	}
	if len(ref) > shareMaxRef {
		return shared.NewAppError("validation_error", "ref must be 200 characters or fewer.", http.StatusUnprocessableEntity)
	}
	return s.shares.Record(ctx, &domain.Share{UserID: userID, EventID: eventID, Channel: channel, Ref: ref})
}
