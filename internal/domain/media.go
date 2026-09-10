package domain

import "time"

// Media kinds and statuses mirror the schema enums.
const (
	MediaKindEventPoster   = "event_poster"
	MediaKindEventTeaser   = "event_teaser"
	MediaKindEventGallery  = "event_gallery"
	MediaKindOrganizerLogo = "organizer_logo"
	MediaKindAvatar        = "avatar"
	MediaKindStory         = "story"

	MediaStatusPending    = "pending"
	MediaStatusProcessing = "processing"
	MediaStatusReady      = "ready"
	MediaStatusFailed     = "failed"
)

// Variant names and densities produced by the worker.
const (
	MediaVariantThumb  = "thumb"
	MediaVariantCard   = "card"
	MediaVariantDetail = "detail"
)

var MediaDensities = []string{"1x", "2x", "3x"}

type MediaAsset struct {
	ID            string
	UploaderID    string
	Kind          string
	StorageBucket string
	StorageKey    string
	ContentType   string
	ByteSize      *int64
	Width         *int
	Height        *int
	Status        string
	ErrorMessage  *string
	DeletedAt     *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type MediaVariant struct {
	ID           string
	MediaAssetID string
	Variant      string
	Density      string
	Format       string
	Width        int
	Height       int
	StorageKey   string
	CDNURL       string
	ByteSize     *int64
	CreatedAt    time.Time
}

type MediaProcessingJob struct {
	ID           string
	MediaAssetID string
	Status       string
	Attempts     int
	NextRetryAt  time.Time
	LastError    *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	StorageKey   string
	ContentType  string
}
