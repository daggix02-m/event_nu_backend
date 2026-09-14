package dto

import "time"

// DeviceRequest registers a client device (FCM token) for push delivery.
type DeviceRequest struct {
	Token    string `json:"token"`
	Platform string `json:"platform"` // "android", "ios", "web"; empty default android
}

type DeviceDTO struct {
	ID         string    `json:"id"`
	Token      string    `json:"token"`
	Platform   string    `json:"platform"`
	LastSeenAt time.Time `json:"last_seen_at"`
	CreatedAt  time.Time `json:"created_at"`
}

type DeviceDeleteResult struct {
	Deleted bool `json:"deleted"`
}
