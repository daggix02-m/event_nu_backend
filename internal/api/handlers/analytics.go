package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
	"github.com/daggix02-m/event_nu/backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/service"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// AnalyticsHandlers expose event ingestion (mobile app) and the admin
// dashboard reads (feature usage + funnels), which aggregate the daily rollup.
type AnalyticsHandlers struct {
	app       *api.Application
	analytics *service.AnalyticsService
}

func NewAnalyticsHandlers(app *api.Application, analytics *service.AnalyticsService) *AnalyticsHandlers {
	return &AnalyticsHandlers{app: app, analytics: analytics}
}

// Ingest accepts a batch of client-side events. user_id attribution never
// comes from the payload: it is the authenticated caller's id (middleware),
// or NULL for anonymous sessions.
func (h *AnalyticsHandlers) Ingest(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())

	var req dto.IngestEventsRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	events := make([]domain.AnalyticsEvent, 0, len(req.Events))
	for _, e := range req.Events {
		if e.Properties == nil {
			e.Properties = map[string]any{}
		}
		events = append(events, domain.AnalyticsEvent{
			SessionID:  e.SessionID,
			EventName:  e.EventName,
			Properties: e.Properties,
		})
	}

	accepted, err := h.analytics.Ingest(r.Context(), userID, events)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusAccepted, dto.IngestEventsResponse{Accepted: accepted})
}

// Usage returns the daily feature-usage rollup for the last N days.
func (h *AnalyticsHandlers) Usage(w http.ResponseWriter, r *http.Request) {
	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil || r.URL.Query().Get("days") == "" {
		days = 14
	}

	rows, err := h.analytics.Usage(r.Context(), days)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}

	resp := dto.UsageResponse{Days: make([]dto.DailyUsageDTO, 0, len(rows))}
	for _, u := range rows {
		resp.Days = append(resp.Days, dto.DailyUsageDTO{
			Day:         u.Day.Format("2006-01-02"),
			EventName:   u.EventName,
			EventCount:  u.EventCount,
			UniqueUsers: u.UniqueUsers,
		})
		resp.TotalEvents += u.EventCount
		resp.TotalUniqueUser += u.UniqueUsers
	}
	shared.WriteJSON(w, http.StatusOK, resp)
}

// FunnelEventCreation aggregates the event-creation funnel over a date range.
func (h *AnalyticsHandlers) FunnelEventCreation(w http.ResponseWriter, r *http.Request) {
	h.funnel(w, r, domain.EventCreationFunnel)
}

// FunnelOrganizerApplication aggregates the organizer-application funnel.
func (h *AnalyticsHandlers) FunnelOrganizerApplication(w http.ResponseWriter, r *http.Request) {
	h.funnel(w, r, domain.OrganizerApplyFunnel)
}

// Onboarding aggregates the onboarding funnel over a date range.
func (h *AnalyticsHandlers) Onboarding(w http.ResponseWriter, r *http.Request) {
	h.funnel(w, r, domain.OnboardingFunnel)
}

func (h *AnalyticsHandlers) funnel(w http.ResponseWriter, r *http.Request, steps []string) {
	from, to, ok := h.parseRange(w, r)
	if !ok {
		return
	}
	agg, err := h.analytics.Funnel(r.Context(), from, to, steps)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.FunnelStepDTO, 0, len(agg))
	for _, s := range agg {
		out = append(out, dto.FunnelStepDTO{
			EventName:   s.EventName,
			EventCount:  s.EventCount,
			UniqueUsers: s.UniqueUsers,
		})
	}
	shared.WriteJSON(w, http.StatusOK, out)
}

// parseRange reads the from/to query params as UTC dates (YYYY-MM-DD). A
// missing or malformed value is a 400; the service also bounds the window.
func (h *AnalyticsHandlers) parseRange(w http.ResponseWriter, r *http.Request) (time.Time, time.Time, bool) {
	from, err := time.Parse("2006-01-02", r.URL.Query().Get("from"))
	if err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "invalid_range", "from must be a YYYY-MM-DD date.")
		return time.Time{}, time.Time{}, false
	}
	to, err := time.Parse("2006-01-02", r.URL.Query().Get("to"))
	if err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "invalid_range", "to must be a YYYY-MM-DD date.")
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}
