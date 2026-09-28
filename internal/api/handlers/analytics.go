package handlers

import (
	"encoding/json"
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
	writeJSONBare(w, http.StatusOK, resp)
}

// DailyMetrics returns the per-day rollup rows the console overview needs. It
// mirrors the Usage response shape (a day series plus window totals) so the
// dashboard can render both the sparkline trend and the summary cards from one
// payload; deltas are computed client-side from consecutive rows.
func (h *AnalyticsHandlers) DailyMetrics(w http.ResponseWriter, r *http.Request) {
	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil || r.URL.Query().Get("days") == "" {
		days = 14
	}

	series, totals, err := h.analytics.DailyMetrics(r.Context(), days)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}

	resp := dto.DailyMetricsResponse{
		Window:   make([]dto.DailyMetricDayDTO, 0, len(series.Window)),
		Previous: make([]dto.DailyMetricDayDTO, 0, len(series.Previous)),
	}
	for _, m := range series.Window {
		resp.Window = append(resp.Window, dto.DailyMetricDayDTO{
			Day:               m.Day.Format("2006-01-02"),
			EventsPublished:   m.EventsPublished,
			EventsCreated:     m.EventsCreated,
			UsersRegistered:   m.UsersRegistered,
			OrganizersCreated: m.OrganizersCreated,
		})
	}
	for _, m := range series.Previous {
		resp.Previous = append(resp.Previous, dto.DailyMetricDayDTO{
			Day:               m.Day.Format("2006-01-02"),
			EventsPublished:   m.EventsPublished,
			EventsCreated:     m.EventsCreated,
			UsersRegistered:   m.UsersRegistered,
			OrganizersCreated: m.OrganizersCreated,
		})
	}
	resp.Totals = dto.WindowTotalsDTO{
		EventsPublished:   totals.EventsPublished,
		EventsCreated:     totals.EventsCreated,
		UsersRegistered:   totals.UsersRegistered,
		OrganizersCreated: totals.OrganizersCreated,
	}
	writeJSONBare(w, http.StatusOK, resp)
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
	writeJSONBare(w, http.StatusOK, out)
}

// writeJSONBare writes v directly without the shared {"data": ...} envelope,
// matching the bare-write convention the events-list handler uses. The admin
// console's analytics pages (usage + funnels) consume the payload at the top
// level through the BFF passthrough, so the writing side must not wrap it.
func writeJSONBare(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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
