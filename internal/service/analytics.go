package service

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/config"
	"github.com/daggix02-m/event_nu_backend/internal/domain"
	"github.com/daggix02-m/event_nu_backend/internal/shared"
)

// analyticsStore is the analytics-repository surface AnalyticsService depends
// on; unit tests stub it with an in-memory recorder.
type analyticsStore interface {
	InsertBatch(ctx context.Context, userID string, events []domain.AnalyticsEvent) (int, error)
	RollupDay(ctx context.Context, day time.Time) (int, error)
	DeleteBefore(ctx context.Context, cutoff time.Time, utcToday time.Time) (int64, error)
	Usage(ctx context.Context, days int) ([]domain.DailyUsage, error)
	DailyMetrics(ctx context.Context, days int) ([]domain.DailyMetricRow, error)
	Funnel(ctx context.Context, from, to time.Time, eventNames []string) ([]domain.FunnelStep, error)
}

// uuidPattern is the accepted shape for session ids (the client generates a
// UUIDv4 once per install/session).
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// AnalyticsService validates and stores first-party analytics events, and runs
// the daily rollup. Validation happens entirely server-side: unknown event
// names or non-empty properties (except search_performed's query_length) are
// rejected so the fixed vocabulary and privacy posture hold even against a
// malicious client.
type AnalyticsService struct {
	analytics analyticsStore
	config    config.Config
}

func NewAnalyticsService(analytics analyticsStore, cfg config.Config) *AnalyticsService {
	return &AnalyticsService{analytics: analytics, config: cfg}
}

// Ingest validates the batch and stores it attributed to userID (empty =
// anonymous). Returns the number of events written (always the full batch on
// success — the whole batch is one atomic statement).
func (s *AnalyticsService) Ingest(ctx context.Context, userID string, events []domain.AnalyticsEvent) (int, error) {
	if len(events) == 0 {
		return 0, shared.NewAppError("empty_batch", "At least one event is required.", http.StatusBadRequest)
	}
	if len(events) > s.config.AnalyticsMaxBatch {
		return 0, shared.NewAppError("too_many_events", "Batch exceeds the maximum event count.", http.StatusBadRequest)
	}
	for _, e := range events {
		if err := s.validateEvent(e); err != nil {
			return 0, err
		}
	}
	return s.analytics.InsertBatch(ctx, userID, events)
}

func (s *AnalyticsService) validateEvent(e domain.AnalyticsEvent) error {
	if !uuidPattern.MatchString(e.SessionID) {
		return shared.NewAppError("invalid_session_id", "session_id must be a valid UUID.", http.StatusBadRequest)
	}
	if !domain.AnalyticsEventNames[e.EventName] {
		return shared.NewAppError("unknown_event", "Event name is not in the supported vocabulary.", http.StatusBadRequest)
	}
	return s.validateProperties(e.EventName, e.Properties)
}

// validateProperties enforces the strict per-event property rules:
// only search_performed carries a property (query_length, an integer), every
// other event must have none. The whole properties blob is also capped in size.
func (s *AnalyticsService) validateProperties(eventName string, props map[string]any) error {
	if len(props) == 0 {
		return nil
	}

	encoded, err := json.Marshal(props)
	if err != nil {
		return shared.NewAppError("invalid_properties", "properties could not be serialized.", http.StatusBadRequest)
	}
	if len(encoded) > s.config.AnalyticsMaxPropertiesBytes {
		return shared.NewAppError("invalid_properties", "properties payload is too large.", http.StatusBadRequest)
	}

	if eventName == domain.EventSearchPerformed {
		if len(props) != 1 {
			return shared.NewAppError("invalid_properties", "search_performed accepts only query_length.", http.StatusBadRequest)
		}
		if _, ok := props["query_length"].(float64); !ok {
			return shared.NewAppError("invalid_properties", "query_length must be a number.", http.StatusBadRequest)
		}
		return nil
	}
	return shared.NewAppError("invalid_properties", "This event does not accept properties.", http.StatusBadRequest)
}

// Usage is the public feature-rollup contract (analytics_daily_feature_usage):
// the per-event daily rollup rows for the last n days, most recent day first.
func (s *AnalyticsService) Usage(ctx context.Context, days int) ([]domain.DailyUsage, error) {
	return s.analytics.Usage(ctx, days)
}

// DailyMetrics is the 2×-window admin contract the Overview renders: the
// current per-day series plus the equal-length prior series, so the console can
// compute real Δ% deltas (no fabricated trends). The repository returns one
// flat 2×window row set (older half first); we split it into the current window
// (the last `days` rows) and the previous window (the first `days` rows) and
// derive the current window's totals — the wire shape the Overview plan §8
// (option A) fixes: {window, previous, totals}.
func (s *AnalyticsService) DailyMetrics(ctx context.Context, days int) (domain.DailyMetricsSeries, domain.WindowTotals, error) {
	if days <= 0 || days > 90 {
		return domain.DailyMetricsSeries{}, domain.WindowTotals{}, shared.NewAppError("invalid_days", "days must be between 1 and 90.", http.StatusUnprocessableEntity)
	}

	rows, err := s.analytics.DailyMetrics(ctx, days)
	if err != nil {
		return domain.DailyMetricsSeries{}, domain.WindowTotals{}, err
	}

	series := domain.DailyMetricsSeries{Window: rows[days:], Previous: rows[:days]}
	var totals domain.WindowTotals
	for _, m := range series.Window {
		totals.EventsPublished += m.EventsPublished
		totals.EventsCreated += m.EventsCreated
		totals.UsersRegistered += m.UsersRegistered
		totals.OrganizersCreated += m.OrganizersCreated
	}
	return series, totals, nil
}

// Funnel returns the per-step aggregates for the named steps over an inclusive
// UTC day range (validated: parsed by the handler, bounded here).
func (s *AnalyticsService) Funnel(ctx context.Context, from, to time.Time, steps []string) ([]domain.FunnelStep, error) {
	if from.IsZero() || to.IsZero() {
		return nil, shared.NewAppError("invalid_range", "from and to are required (YYYY-MM-DD).", http.StatusBadRequest)
	}
	if from.After(to) {
		return nil, shared.NewAppError("invalid_range", "from must not be after to.", http.StatusBadRequest)
	}
	if to.Sub(from) > 90*24*time.Hour {
		return nil, shared.NewAppError("invalid_range", "date range must not exceed 90 days.", http.StatusBadRequest)
	}
	agg, err := s.analytics.Funnel(ctx, from, to, steps)
	if err != nil {
		return nil, err
	}
	// Zero-fill steps with no rows so the dashboard renders a stable funnel.
	byName := make(map[string]domain.FunnelStep, len(agg))
	for _, a := range agg {
		byName[a.EventName] = a
	}
	out := make([]domain.FunnelStep, 0, len(steps))
	for _, step := range steps {
		if v, ok := byName[step]; ok {
			out = append(out, v)
			continue
		}
		out = append(out, domain.FunnelStep{EventName: step})
	}
	return out, nil
}
