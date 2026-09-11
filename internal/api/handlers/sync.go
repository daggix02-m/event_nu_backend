package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/service"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// SyncHandlers serves GET /api/v1/sync, the cursor-based offline delta (Phase
// 16). The endpoint is authenticated so per-user RLS scopes every row; the
// payload carries only public catalog content.
type SyncHandlers struct {
	app  *api.Application
	sync *service.SyncService
}

func NewSyncHandlers(app *api.Application, sync *service.SyncService) *SyncHandlers {
	return &SyncHandlers{app: app, sync: sync}
}

// Pull streams changes since the cursor across the requested domains.
func (h *SyncHandlers) Pull(w http.ResponseWriter, r *http.Request) {
	opts, ok := parseSyncParams(w, r, h.app.Config.SyncMaxLimit)
	if !ok {
		return
	}
	res, err := h.sync.Pull(r.Context(), opts)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}

	changes := make([]dto.SyncChange, 0, len(res.Changes))
	for _, c := range res.Changes {
		changes = append(changes, dto.SyncChange{
			Domain:  c.Domain,
			Op:      c.Op,
			ID:      c.ID,
			Payload: toSyncPayload(c),
		})
	}

	var hasMore map[string]bool
	for domain, more := range res.HasMore {
		if more {
			if hasMore == nil {
				hasMore = make(map[string]bool, len(res.HasMore))
			}
			hasMore[domain] = true
		}
	}

	shared.WriteJSON(w, http.StatusOK, dto.SyncData{
		Changes:    changes,
		NextCursor: res.NextCursor.Format(time.RFC3339Nano),
		HasMore:    hasMore,
	})
}

// parseSyncParams reads and validates cursor/domains/limit. Cursor defaults to
// the epoch (a full bootstrap); domains default to the full whitelist; limit
// defaults to the configured maximum.
func parseSyncParams(w http.ResponseWriter, r *http.Request, maxLimit int) (service.PullOptions, bool) {
	q := r.URL.Query()
	var opts service.PullOptions

	if v := q.Get("cursor"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			shared.WriteErrorJSON(w, http.StatusBadRequest, "bad_request", "cursor must be an RFC3339 timestamp")
			return opts, false
		}
		opts.Cursor = t
	}

	if v := q.Get("domains"); v != "" {
		for _, d := range strings.Split(v, ",") {
			d = strings.TrimSpace(d)
			if _, ok := service.SyncWhitelist[d]; !ok {
				shared.WriteErrorJSON(w, http.StatusBadRequest, "bad_request", "unknown sync domain: "+d)
				return opts, false
			}
			opts.Domains = append(opts.Domains, d)
		}
	}

	opts.Limit = maxLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxLimit {
			shared.WriteErrorJSON(w, http.StatusBadRequest, "bad_request",
				fmt.Sprintf("limit must be between 1 and %d", maxLimit))
			return opts, false
		}
		opts.Limit = n
	}

	return opts, true
}

// toSyncPayload maps a change row to its wire DTO. Delete ops carry no payload
// (the id is enough); upserts use the same shapes the plain CRUD endpoints
// expose, minus per-user decorations (like/save state, follower counts, media
// URLs) which the client re-fetches via the private domains on reconnect.
func toSyncPayload(c service.Change) any {
	if c.Op == "delete" {
		return nil
	}
	switch row := c.Row.(type) {
	case *domain.Event:
		return toEventDTO(row)
	case *domain.Venue:
		return toVenueDTO(row)
	case *domain.Organizer:
		return dto.OrganizerDTO{
			ID:        row.ID,
			Slug:      row.Slug,
			Name:      row.Name,
			Bio:       row.Bio,
			Status:    row.Status,
			CreatedAt: row.CreatedAt,
		}
	case *domain.Category:
		return dto.CategoryDTO{ID: row.ID, Slug: row.Slug, Name: row.Name}
	case *domain.TicketType:
		now := time.Now()
		soldOut := row.QuantityTotal != nil && row.QuantitySold >= *row.QuantityTotal
		open := (row.SalesStart == nil || !now.Before(*row.SalesStart)) &&
			(row.SalesEnd == nil || !now.After(*row.SalesEnd))
		return toTicketTypeDTO(row, soldOut, open)
	case *domain.Comment:
		return toCommentDTO(row)
	case *domain.Review:
		return toReviewDTO(row)
	}
	return nil
}
