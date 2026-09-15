package dto

import (
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
)

type RecapMomentDTO struct {
	ID           string    `json:"id"`
	Caption      string    `json:"caption"`
	MediaAssetID string    `json:"media_asset_id"`
	CreatedAt    time.Time `json:"created_at"`
}

type RecapSessionDTO struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Speaker  *string   `json:"speaker,omitempty"`
	Stage    *string   `json:"stage,omitempty"`
	StartsAt time.Time `json:"starts_at"`
}

// RecapDTO is the public dashboard a published event presents: featured
// moments, attendance, review summary, and schedule highlights, plus when it
// was last generated and whether that read was served from the cache.
type RecapDTO struct {
	MomentCount       int               `json:"moment_count"`
	TopMoments        []RecapMomentDTO  `json:"top_moments"`
	AttendeeCount     int               `json:"attendee_count"`
	ReviewCount       int               `json:"review_count"`
	ReviewAverage     *float64          `json:"review_average,omitempty"`
	SessionCount      int               `json:"session_count"`
	SessionHighlights []RecapSessionDTO `json:"session_highlights"`
	GeneratedAt       time.Time         `json:"generated_at"`
	Cached            bool              `json:"cached"`
}

func ToRecapDTO(res *domain.RecapResult) RecapDTO {
	out := RecapDTO{
		MomentCount:   res.Data.MomentCount,
		AttendeeCount: res.Data.AttendeeCount,
		ReviewCount:   res.Data.ReviewCount,
		ReviewAverage: res.Data.ReviewAverage,
		SessionCount:  res.Data.SessionCount,
		GeneratedAt:   res.GeneratedAt,
		Cached:        res.Cached,
	}
	out.TopMoments = make([]RecapMomentDTO, 0, len(res.Data.TopMoments))
	for _, m := range res.Data.TopMoments {
		out.TopMoments = append(out.TopMoments, RecapMomentDTO{
			ID:           m.ID,
			Caption:      m.Caption,
			MediaAssetID: m.MediaAssetID,
			CreatedAt:    m.CreatedAt,
		})
	}
	out.SessionHighlights = make([]RecapSessionDTO, 0, len(res.Data.SessionHighlights))
	for _, s := range res.Data.SessionHighlights {
		out.SessionHighlights = append(out.SessionHighlights, RecapSessionDTO{
			ID:       s.ID,
			Title:    s.Title,
			Speaker:  s.Speaker,
			Stage:    s.Stage,
			StartsAt: s.StartsAt,
		})
	}
	return out
}
