package dto

import (
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
)

// NotificationDTO is the wire form of a notification inbox entry.
type NotificationDTO struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Title     string         `json:"title"`
	Body      string         `json:"body"`
	Data      map[string]any `json:"data"`
	ReadAt    *time.Time     `json:"read_at,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

func NewNotificationDTO(n *domain.Notification) NotificationDTO {
	return NotificationDTO{
		ID:        n.ID,
		Type:      n.Type,
		Title:     n.Title,
		Body:      n.Body,
		Data:      n.Data,
		ReadAt:    n.ReadAt,
		CreatedAt: n.CreatedAt,
	}
}

// NotificationReadResult confirms a single mark-as-read (idempotent).
type NotificationReadResult struct {
	Read bool `json:"read"`
}

// NotificationReadAllResult reports how many notifications were marked read.
type NotificationReadAllResult struct {
	Updated int64 `json:"updated"`
}

// ReminderRequest sets a reminder; remind_at is an RFC3339 timestamp that must
// land before the event starts.
type ReminderRequest struct {
	RemindAt time.Time `json:"remind_at"`
}

// ReminderDTO is the wire form of a reminder with its event summary.
type ReminderDTO struct {
	ID         string     `json:"id"`
	EventID    string     `json:"event_id"`
	EventTitle string     `json:"event_title"`
	EventTime  *time.Time `json:"event_time,omitempty"`
	RemindAt   time.Time  `json:"remind_at"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
}

func NewReminderDTO(r *domain.ReminderWithEvent) ReminderDTO {
	return ReminderDTO{
		ID:         r.ID,
		EventID:    r.EventID,
		EventTitle: r.EventTitle,
		EventTime:  r.EventTime,
		RemindAt:   r.RemindAt,
		Status:     r.Status,
		CreatedAt:  r.CreatedAt,
	}
}

// AdminReportDTO is the admin-facing report queue item.
type AdminReportDTO struct {
	ID          string     `json:"id"`
	ReporterID  string     `json:"reporter_user_id"`
	EntityType  string     `json:"entity_type"`
	EntityID    string     `json:"entity_id"`
	ReasonCode  string     `json:"reason_code"`
	Description string     `json:"description,omitempty"`
	Status      string     `json:"status"`
	Resolution  *string    `json:"resolution,omitempty"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func NewAdminReportDTO(r *domain.Report) AdminReportDTO {
	return AdminReportDTO{
		ID:          r.ID,
		ReporterID:  r.ReporterUserID,
		EntityType:  r.EntityType,
		EntityID:    r.EntityID,
		ReasonCode:  r.ReasonCode,
		Description: r.Description,
		Status:      r.Status,
		Resolution:  r.Resolution,
		ResolvedAt:  r.ResolvedAt,
		CreatedAt:   r.CreatedAt,
	}
}

// ResolveRequest optionally records why a report was closed.
type ResolveRequest struct {
	Resolution string `json:"resolution"`
}

// VenueDetailDTO embeds a venue with its published-event page.
type VenueDetailDTO struct {
	Venue             VenueDTO   `json:"venue"`
	Events            []EventDTO `json:"events"`
	EventsTotal       int        `json:"events_total"`
	EventsPage        int        `json:"events_page"`
	EventsPageSize    int        `json:"events_page_size"`
	EventsHasNextPage bool       `json:"events_has_next_page"`
}

// VenueUpdateRequest carries the editable venue fields (PATCH semantics: only
// fields that can change are present).
type VenueUpdateRequest struct {
	Name        string  `json:"name"`
	Address     string  `json:"address"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	PlaceID     string  `json:"place_id"`
	City        string  `json:"city"`
	CountryCode string  `json:"country_code"`
}
