package dto

// IngestEvent is one inbound analytics event. Properties is deliberately a
// free-form map so payload size / key validation can live in the service.
type IngestEvent struct {
	SessionID  string         `json:"session_id"`
	EventName  string         `json:"event_name"`
	Properties map[string]any `json:"properties"`
}

type IngestEventsRequest struct {
	Events []IngestEvent `json:"events"`
}

// IngestEventsResponse reports how many events were accepted (the whole batch
// on success; the batch is a single atomic statement).
type IngestEventsResponse struct {
	Accepted int `json:"accepted"`
}

type DailyUsageDTO struct {
	Day         string `json:"day"`
	EventName   string `json:"event_name"`
	EventCount  int64  `json:"event_count"`
	UniqueUsers int64  `json:"unique_users"`
}

type UsageResponse struct {
	Days            []DailyUsageDTO `json:"days"`
	TotalEvents     int64           `json:"total_events"`
	TotalUniqueUser int64           `json:"total_unique_users"`
}

type FunnelStepDTO struct {
	EventName   string `json:"event_name"`
	EventCount  int64  `json:"event_count"`
	UniqueUsers int64  `json:"unique_users"`
}

// DailyMetricDayDTO is one day in the admin daily-metrics series. All four
// metrics are real business-table day counts (see domain.DailyMetricRow); no
// fabricated deltas or status-based approximations on the wire.
type DailyMetricDayDTO struct {
	Day               string `json:"day"`
	EventsPublished   int64  `json:"events_published"`
	EventsCreated     int64  `json:"events_created"`
	UsersRegistered   int64  `json:"users_registered"`
	OrganizersCreated int64  `json:"organizers_created"`
}

// WindowTotalsDTO aggregates each metric across one admin window.
type WindowTotalsDTO struct {
	EventsPublished   int64 `json:"events_published"`
	EventsCreated     int64 `json:"events_created"`
	UsersRegistered   int64 `json:"users_registered"`
	OrganizersCreated int64 `json:"organizers_created"`
}

// DailyMetricsResponse is the admin Overview's contract (§8 option A):
// a 2×-window day series plus the current window's totals.
type DailyMetricsResponse struct {
	Window   []DailyMetricDayDTO `json:"window"`
	Previous []DailyMetricDayDTO `json:"previous"`
	Totals   WindowTotalsDTO     `json:"totals"`
}
