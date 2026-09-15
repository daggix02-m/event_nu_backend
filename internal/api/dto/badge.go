package dto

import "time"

type BadgeDTO struct {
	ID        string         `json:"id"`
	BadgeType string         `json:"badge_type"`
	EarnedAt  time.Time      `json:"earned_at"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}
