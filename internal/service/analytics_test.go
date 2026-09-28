package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
)

type stubAnalyticsStore struct {
	insertedUser string
	inserted     []domain.AnalyticsEvent
	usage        []domain.DailyUsage
	dailyMetrics []domain.DailyMetricRow
	funnel       []domain.FunnelStep
}

func (s *stubAnalyticsStore) DailyMetrics(_ context.Context, _ int) ([]domain.DailyMetricRow, error) {
	return s.dailyMetrics, nil
}

func (s *stubAnalyticsStore) InsertBatch(_ context.Context, userID string, events []domain.AnalyticsEvent) (int, error) {
	s.insertedUser = userID
	s.inserted = append(s.inserted, events...)
	return len(events), nil
}

func (s *stubAnalyticsStore) RollupDay(_ context.Context, _ time.Time) (int, error) { return 0, nil }
func (s *stubAnalyticsStore) DeleteBefore(_ context.Context, _, _ time.Time) (int64, error) {
	return 0, nil
}
func (s *stubAnalyticsStore) Usage(_ context.Context, _ int) ([]domain.DailyUsage, error) {
	return s.usage, nil
}
func (s *stubAnalyticsStore) Funnel(_ context.Context, _, _ time.Time, _ []string) ([]domain.FunnelStep, error) {
	return s.funnel, nil
}

func analyticsCfg() config.Config {
	c := config.Config{
		AnalyticsMaxBatch:           100,
		AnalyticsMaxPropertiesBytes: 2048,
		AnalyticsRetentionDays:      90,
	}
	return c
}

const validSession = "550e8400-e29b-41d4-a716-446655440000"

func evt(session, name string, props map[string]any) domain.AnalyticsEvent {
	return domain.AnalyticsEvent{SessionID: session, EventName: name, Properties: props}
}

func TestAnalyticsIngestValidBatch(t *testing.T) {
	store := &stubAnalyticsStore{}
	svc := NewAnalyticsService(store, analyticsCfg())

	events := []domain.AnalyticsEvent{
		evt(validSession, domain.EventScreenViewed, nil),
		evt(validSession, domain.EventSearchPerformed, map[string]any{"query_length": float64(6)}),
	}
	got, err := svc.Ingest(context.Background(), "u1", events)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if got != 2 {
		t.Fatalf("expected 2 accepted, got %d", got)
	}
	if store.insertedUser != "u1" {
		t.Fatalf("expected attribution to u1, got %q", store.insertedUser)
	}
}

func TestAnalyticsIngestAnonymous(t *testing.T) {
	store := &stubAnalyticsStore{}
	svc := NewAnalyticsService(store, analyticsCfg())
	if _, err := svc.Ingest(context.Background(), "", []domain.AnalyticsEvent{evt(validSession, domain.EventScreenViewed, nil)}); err != nil {
		t.Fatalf("anonymous ingest: %v", err)
	}
	if store.insertedUser != "" {
		t.Fatalf("expected anonymous attribution (empty user), got %q", store.insertedUser)
	}
}

func TestAnalyticsIngestRejectsEmptyBatch(t *testing.T) {
	svc := NewAnalyticsService(&stubAnalyticsStore{}, analyticsCfg())
	_, err := svc.Ingest(context.Background(), "", nil)
	if !isAppErrorStatus(err, "empty_batch", 400) {
		t.Fatalf("expected 400 empty_batch, got %v", err)
	}
}

func TestAnalyticsIngestRejectsOversizedBatch(t *testing.T) {
	cfg := analyticsCfg()
	cfg.AnalyticsMaxBatch = 2
	svc := NewAnalyticsService(&stubAnalyticsStore{}, cfg)
	events := []domain.AnalyticsEvent{
		evt(validSession, domain.EventScreenViewed, nil),
		evt(validSession, domain.EventScreenViewed, nil),
		evt(validSession, domain.EventScreenViewed, nil),
	}
	_, err := svc.Ingest(context.Background(), "", events)
	if !isAppErrorStatus(err, "too_many_events", 400) {
		t.Fatalf("expected 400 too_many_events, got %v", err)
	}
}

func TestAnalyticsIngestRejectsBadSession(t *testing.T) {
	svc := NewAnalyticsService(&stubAnalyticsStore{}, analyticsCfg())
	_, err := svc.Ingest(context.Background(), "", []domain.AnalyticsEvent{evt("not-a-uuid", domain.EventScreenViewed, nil)})
	if !isAppErrorStatus(err, "invalid_session_id", 400) {
		t.Fatalf("expected 400 invalid_session_id, got %v", err)
	}
}

func TestAnalyticsIngestRejectsUnknownEvent(t *testing.T) {
	svc := NewAnalyticsService(&stubAnalyticsStore{}, analyticsCfg())
	_, err := svc.Ingest(context.Background(), "", []domain.AnalyticsEvent{evt(validSession, "totally_made_up", nil)})
	if !isAppErrorStatus(err, "unknown_event", 400) {
		t.Fatalf("expected 400 unknown_event, got %v", err)
	}
}

func TestAnalyticsIngestPropertiesRules(t *testing.T) {
	svc := NewAnalyticsService(&stubAnalyticsStore{}, analyticsCfg())

	// Properties on non-search events are rejected.
	_, err := svc.Ingest(context.Background(), "", []domain.AnalyticsEvent{
		evt(validSession, domain.EventScreenViewed, map[string]any{"screen": "home"}),
	})
	if !isAppErrorStatus(err, "invalid_properties", 400) {
		t.Fatalf("expected 400 invalid_properties for event with props, got %v", err)
	}

	// search_performed with extra keys rejected.
	_, err = svc.Ingest(context.Background(), "", []domain.AnalyticsEvent{
		evt(validSession, domain.EventSearchPerformed, map[string]any{"query_length": float64(5), "query_text": "hi"}),
	})
	if !isAppErrorStatus(err, "invalid_properties", 400) {
		t.Fatalf("expected 400 invalid_properties for extra search key, got %v", err)
	}

	// query_length must be a number.
	_, err = svc.Ingest(context.Background(), "", []domain.AnalyticsEvent{
		evt(validSession, domain.EventSearchPerformed, map[string]any{"query_length": "six"}),
	})
	if !isAppErrorStatus(err, "invalid_properties", 400) {
		t.Fatalf("expected 400 invalid_properties for wrong query_length type, got %v", err)
	}
}

func TestAnalyticsIngestRejectsOversizedProperties(t *testing.T) {
	cfg := analyticsCfg()
	cfg.AnalyticsMaxPropertiesBytes = 16
	svc := NewAnalyticsService(&stubAnalyticsStore{}, cfg)
	_, err := svc.Ingest(context.Background(), "", []domain.AnalyticsEvent{
		evt(validSession, domain.EventSearchPerformed, map[string]any{"query_length": float64(123456789)}),
	})
	if !isAppErrorStatus(err, "invalid_properties", 400) {
		t.Fatalf("expected 400 invalid_properties for oversized payload, got %v", err)
	}
}

func TestAnalyticsFunnelZeroFillsMissingSteps(t *testing.T) {
	store := &stubAnalyticsStore{funnel: []domain.FunnelStep{{EventName: domain.EventEventCreateStarted, EventCount: 3}}}
	svc := NewAnalyticsService(store, analyticsCfg())
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	steps, err := svc.Funnel(context.Background(), from, to, domain.EventCreationFunnel)
	if err != nil {
		t.Fatalf("funnel: %v", err)
	}
	if len(steps) != len(domain.EventCreationFunnel) {
		t.Fatalf("expected %d steps (zero-filled), got %d", len(domain.EventCreationFunnel), len(steps))
	}
	for _, s := range steps {
		if s.EventCount < 0 {
			t.Fatalf("negative count for %s", s.EventName)
		}
	}
}

func TestAnalyticsFunnelValidatesRange(t *testing.T) {
	svc := NewAnalyticsService(&stubAnalyticsStore{}, analyticsCfg())
	from := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	_, err := svc.Funnel(context.Background(), from, to, domain.OnboardingFunnel)
	if !isAppErrorStatus(err, "invalid_range", 400) {
		t.Fatalf("expected 400 invalid_range for inverted dates, got %v", err)
	}
	// > 90 days.
	_, err = svc.Funnel(context.Background(),
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		domain.OnboardingFunnel)
	if !isAppErrorStatus(err, "invalid_range", 400) {
		t.Fatalf("expected 400 invalid_range for >90 days, got %v", err)
	}
}

func TestAnalyticsIngestRejectsNonVocabularyInAnyBatch(t *testing.T) {
	svc := NewAnalyticsService(&stubAnalyticsStore{}, analyticsCfg())
	events := []domain.AnalyticsEvent{
		evt(validSession, domain.EventScreenViewed, nil),
		evt(validSession, strings.TrimSpace(" onboarding_sneaky "), nil),
	}
	_, err := svc.Ingest(context.Background(), "", events)
	if !isAppErrorStatus(err, "unknown_event", 400) {
		t.Fatalf("expected 400 unknown_event, got %v", err)
	}
}
