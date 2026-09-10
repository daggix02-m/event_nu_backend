package dto

import "time"

type UploadIntentRequest struct {
	Kind        string `json:"kind"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

type UploadIntentResponse struct {
	ID                  string `json:"id"`
	Kind                string `json:"kind"`
	Status              string `json:"status"`
	UploadURL           string `json:"upload_url"`
	UploadExpirySeconds int    `json:"upload_expiry_seconds"`
}

type MediaVariantDTO struct {
	Variant  string `json:"variant"`
	Density  string `json:"density"`
	Format   string `json:"format"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	URL      string `json:"url"`
	ByteSize int64  `json:"byte_size"`
}

type MediaAssetDTO struct {
	ID            string            `json:"id"`
	Kind          string            `json:"kind"`
	Status        string            `json:"status"`
	ContentType   string            `json:"content_type"`
	StorageBucket string            `json:"storage_bucket"`
	Width         *int              `json:"width"`
	Height        *int              `json:"height"`
	ByteSize      *int64            `json:"byte_size"`
	ErrorMessage  *string           `json:"error_message"`
	CreatedAt     time.Time         `json:"created_at"`
	Variants      []MediaVariantDTO `json:"variants"`
}
