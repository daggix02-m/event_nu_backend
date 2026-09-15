package domain

import "time"

// RecapMoment is one of the community moments featured in an event recap.
type RecapMoment struct {
	ID           string    `json:"id"`
	Caption      string    `json:"caption"`
	MediaAssetID string    `json:"media_asset_id"`
	CreatedAt    time.Time `json:"created_at"`
}

// RecapSession is one of the schedule highlights featured in an event recap.
type RecapSession struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Speaker  *string   `json:"speaker,omitempty"`
	Stage    *string   `json:"stage,omitempty"`
	StartsAt time.Time `json:"starts_at"`
}

// RecapData is the aggregated, cacheable dashboard for a published event. Its
// JSON shape is what event_recaps.data stores and the recap endpoint returns.
type RecapData struct {
	MomentCount       int            `json:"moment_count"`
	TopMoments        []RecapMoment  `json:"top_moments"`
	AttendeeCount     int            `json:"attendee_count"`
	ReviewCount       int            `json:"review_count"`
	ReviewAverage     *float64       `json:"review_average,omitempty"`
	SessionCount      int            `json:"session_count"`
	SessionHighlights []RecapSession `json:"session_highlights"`
}

// EventRecap is a cached recap row for an event.
type EventRecap struct {
	EventID     string
	Data        RecapData
	GeneratedAt time.Time
}

// RecapResult is a computed (or cache-served) recap with its provenance.
type RecapResult struct {
	Data        RecapData
	GeneratedAt time.Time
	Cached      bool
}
