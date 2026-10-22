// Package transportgrpc is the gRPC transport layer (Phase 3). It exposes the
// same application services the chi REST layer uses, so the core has no
// knowledge of which transport is calling it. Mapping rules:
//
//   - domain/shared errors → gRPC status codes (errors.go), never raw error
//     strings for internal failures;
//   - pagination and id validation at the edge, like the REST handlers;
//   - per-user enrichment (likes/saves) needs identity, which arrives with
//     the auth slice; today the EventService RPCs are identity-free.
package transportgrpc

import (
	"context"
	"log/slog"
	"regexp"

	"github.com/daggix02-m/event_nu_backend/internal/domain"
	"github.com/daggix02-m/event_nu_backend/internal/service"
	"github.com/daggix02-m/event_nu_backend/internal/transport/grpc/eventnupb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var validUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`).MatchString

type mediaResolver interface {
	ResolveEventMedia(ctx context.Context, posterMediaID, teaserMediaID *string) (string, string, error)
}

type likeStateChecker interface {
	State(ctx context.Context, eventID, userID string) (int, bool, error)
}

type saveStateChecker interface {
	State(ctx context.Context, userID, eventID string) (bool, *string, error)
}

type EventServer struct {
	eventnupb.UnimplementedEventServiceServer
	event  *service.EventService
	media  mediaResolver
	likes  likeStateChecker
	saves  saveStateChecker
	logger *slog.Logger
}

func NewEventServer(event *service.EventService, media mediaResolver, likes likeStateChecker, saves saveStateChecker, logger *slog.Logger) *EventServer {
	return &EventServer{event: event, media: media, likes: likes, saves: saves, logger: logger}
}

func (s *EventServer) GetEvent(ctx context.Context, req *eventnupb.GetEventRequest) (*eventnupb.Event, error) {
	id, ok := req.GetId(), true
	if !validUUID(req.GetId()) {
		ok = false
	}
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	e, err := s.event.GetEvent(ctx, id)
	if err != nil {
		return nil, toStatus(err)
	}
	return s.toEventDTO(ctx, e), nil
}

func (s *EventServer) ListEvents(ctx context.Context, req *eventnupb.ListEventsRequest) (*eventnupb.ListEventsResponse, error) {
	page := int(req.GetPage())
	limit := int(req.GetLimit())
	if page < 1 {
		page = 1
	}
	if limit == 0 {
		limit = 20
	}
	if limit > 100 {
		return nil, status.Error(codes.InvalidArgument, "limit must be at most 100")
	}

	filters := domain.EventFilters{
		Query:      req.GetQuery(),
		CategoryID: req.GetCategoryId(),
	}
	if req.GetCategoryId() != "" {
		if !validUUID(req.GetCategoryId()) {
			return nil, status.Error(codes.InvalidArgument, "category_id must be a valid UUID")
		}
	}

	result, err := s.event.ListEvents(ctx, filters, page, limit)
	if err != nil {
		return nil, toStatus(err)
	}
	out := make([]*eventnupb.Event, 0, len(result.Items))
	for _, e := range result.Items {
		out = append(out, s.toEventDTO(ctx, e))
	}
	return &eventnupb.ListEventsResponse{
		Events: out,
		Total:  int64(result.Total),
	}, nil
}

func (s *EventServer) ListMyEvents(ctx context.Context, req *eventnupb.ListMyEventsRequest) (*eventnupb.ListMyEventsResponse, error) {
	userID := UserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	page := int(req.GetPage())
	limit := int(req.GetLimit())
	if page < 1 {
		page = 1
	}
	if limit == 0 {
		limit = 20
	}
	if limit > 100 {
		return nil, status.Error(codes.InvalidArgument, "limit must be at most 100")
	}

	result, err := s.event.ListMyEvents(ctx, userID, req.GetStatus(), page, limit)
	if err != nil {
		return nil, toStatus(err)
	}
	out := make([]*eventnupb.Event, 0, len(result.Items))
	for _, e := range result.Items {
		out = append(out, s.toEventDTO(ctx, e))
	}
	return &eventnupb.ListMyEventsResponse{
		Events: out,
		Total:  int64(result.Total),
	}, nil
}

func (s *EventServer) toEventDTO(ctx context.Context, e *domain.Event) *eventnupb.Event {
	out := &eventnupb.Event{
		Id:               e.ID,
		OrganizerId:      e.OrganizerID,
		VenueId:          e.VenueID,
		CategoryId:       e.CategoryID,
		Title:            e.Title,
		Description:      e.Description,
		StartsAt:         timestamp(e.StartsAt),
		EndsAt:           timestampOptional(e.EndsAt),
		PriceIsFree:      e.PriceIsFree,
		PriceDisplay:     e.PriceDisplay,
		ActionType:       e.ActionType,
		Status:           e.Status,
		ModerationStatus: e.ModerationStatus,
		MaxAttendees:     maxAttendees(e.MaxAttendees),
		CreatedAt:        timestamp(e.CreatedAt),
	}
	if e.PosterMediaID != nil || e.TeaserMediaID != nil {
		if s.media != nil {
			poster, teaser, merr := s.media.ResolveEventMedia(ctx, e.PosterMediaID, e.TeaserMediaID)
			if merr != nil {
				if s.logger != nil {
					s.logger.Warn("event media resolution unavailable", "event_id", e.ID, "error", merr.Error())
				}
			} else {
				out.PosterUrl, out.TeaserUrl = poster, teaser
			}
		}
	}

	userID := UserID(ctx)
	if s.likes != nil {
		count, liked, lerr := s.likes.State(ctx, e.ID, userID)
		if lerr != nil {
			if s.logger != nil {
				s.logger.Warn("event-like state unavailable", "event_id", e.ID, "error", lerr.Error())
			}
		} else {
			out.LikeCount = int64(count)
			out.LikedByMe = liked
		}
	}
	if s.saves != nil {
		saved, folderID, serr := s.saves.State(ctx, userID, e.ID)
		if serr != nil {
			if s.logger != nil {
				s.logger.Warn("event-save state unavailable", "event_id", e.ID, "error", serr.Error())
			}
		} else {
			out.SavedByMe = saved
			out.SavedFolderId = folderID
		}
	}
	return out
}
