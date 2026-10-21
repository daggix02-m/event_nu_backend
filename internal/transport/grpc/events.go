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

// validUUID matches the canonical 8-4-4-4-12 hex form PostgreSQL's
// gen_random_uuid() emits (any case). Ids are plain strings in this project
// (no uuid dependency); the strict check runs before any DB call so a
// malformed id can never surface as a 22P02 internal error. The REST handlers
// enforce the same shape (internal/api/handlers/param.go).
var validUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`).MatchString

// mediaResolver is the consumer-side view of MediaService the gRPC transport
// needs (interface segregation: one method, no more).
type mediaResolver interface {
	ResolveEventMedia(ctx context.Context, posterMediaID, teaserMediaID *string) (string, string, error)
}

// EventServer serves eventnu.v1.EventService over gRPC from the application's
// EventService and MediaService. It is wired in cmd/api alongside the REST
// server; tests drive it through bufconn.
type EventServer struct {
	eventnupb.UnimplementedEventServiceServer
	event  *service.EventService
	media  mediaResolver
	logger *slog.Logger
}

// NewEventServer wires the gRPC event server from the services it shares with
// the REST transport; main is the composition root that passes them.
func NewEventServer(event *service.EventService, media mediaResolver, logger *slog.Logger) *EventServer {
	return &EventServer{event: event, media: media, logger: logger}
}

// GetEvent returns one event by id.
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

// ListEvents pages the public catalog.
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

// toEventDTO maps a domain event to the wire message. Media resolution is a
// soft-fail: an unavailable media service degrades to empty URLs instead of
// failing the read (same posture as the REST enrichment).
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
	return out
}
