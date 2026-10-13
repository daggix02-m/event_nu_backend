package domain

import "time"

// Fixed analytics event vocabulary. The full catalog is maintained internally;
// server and client both mirror THIS set. Unknown names are rejected at
// ingest, never silently dropped.
const (
	EventScreenViewed            = "screen_view"
	EventEventListViewed         = "event_list_viewed"
	EventEventDetailViewed       = "event_detail_viewed"
	EventEventCreateStarted      = "event_create_started"
	EventEventCreateCompleted    = "event_create_completed"
	EventEventPublishCompleted   = "event_publish_completed"
	EventOrganizerApplyStarted   = "organizer_apply_started"
	EventOrganizerApplyCompleted = "organizer_apply_completed"
	EventSearchPerformed         = "search_performed"
	EventCategoryFilterApplied   = "category_filter_applied"
	EventOnboardingStepViewed    = "onboarding_step_viewed"
	EventOnboardingCompleted     = "onboarding_completed"
	EventOnboardingSkipped       = "onboarding_skipped"
)

// AnalyticsEventNames is the server-side source of truth for accepted names.
var AnalyticsEventNames = map[string]bool{
	EventScreenViewed:            true,
	EventEventListViewed:         true,
	EventEventDetailViewed:       true,
	EventEventCreateStarted:      true,
	EventEventCreateCompleted:    true,
	EventEventPublishCompleted:   true,
	EventOrganizerApplyStarted:   true,
	EventOrganizerApplyCompleted: true,
	EventSearchPerformed:         true,
	EventCategoryFilterApplied:   true,
	EventOnboardingStepViewed:    true,
	EventOnboardingCompleted:     true,
	EventOnboardingSkipped:       true,
}

// Funnel definitions used by the admin dashboard. Each step lists the event
// names it aggregates over the requested day range.
var (
	EventCreationFunnel  = []string{EventEventCreateStarted, EventEventCreateCompleted, EventEventPublishCompleted}
	OrganizerApplyFunnel = []string{EventOrganizerApplyStarted, EventOrganizerApplyCompleted}
	OnboardingFunnel     = []string{EventOnboardingStepViewed, EventOnboardingCompleted, EventOnboardingSkipped}
)

// AnalyticsEvent is a single inbound event before attribution: session id and
// properties come from the client; user + timestamp are added by the server.
type AnalyticsEvent struct {
	SessionID  string
	EventName  string
	Properties map[string]any
}

// DailyUsage is one (day, event_name) rollup row.
type DailyUsage struct {
	Day         time.Time
	EventName   string
	EventCount  int64
	UniqueUsers int64
}

// FunnelStep is a funnel stage's aggregate over a day range.
type FunnelStep struct {
	EventName   string
	EventCount  int64
	UniqueUsers int64
}

// DailyMetricRow is one day in the admin daily-metrics series. The first four
// metrics are derived from the business tables (events / users / organizers),
// while EventsPublished reads the analytics rollup's event_publish_completed
// signal — the one honest per-day "published" source (events carry no
// published_at timestamp, so a status-based day count would be fabricated).
type DailyMetricRow struct {
	Day               time.Time
	EventsPublished   int64
	EventsCreated     int64
	UsersRegistered   int64
	OrganizersCreated int64
}

// DailyMetricsSeries is the 2×-window view the admin Overview renders: the
// current window's day series plus the equal-length prior window, so the
// console can compute genuine Δ% deltas (no fabricated trends).
type DailyMetricsSeries struct {
	Window   []DailyMetricRow
	Previous []DailyMetricRow
}

// WindowTotals aggregates each metric across one window (used for both the
// current and the prior window; the client derives the delta between them).
type WindowTotals struct {
	EventsPublished   int64
	EventsCreated     int64
	UsersRegistered   int64
	OrganizersCreated int64
}
