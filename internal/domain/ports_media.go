package domain

import (
	"context"
	"io"
	"time"
)

// ObjectStore is the storage port the media pipeline needs (hexagonal slice
// 4, moved from the infrastructure package): the core describes presigned
// uploads, public read URLs, and object lifecycle; the local and S3 adapters
// in package storage implement it.
type ObjectStore interface {
	// Bucket reports the bucket/directory name rows store in storage_bucket.
	Bucket() string

	// PresignUpload returns a URL the client can PUT the object to within
	// expiry. The key is server-generated; a presigned URL never grants more
	// than one key.
	PresignUpload(ctx context.Context, key, contentType string, expiry time.Duration) (string, error)

	// PublicReadURL builds the stable, publicly fetchable URL for a key. It is
	// stored on media rows and surfaced as cdn_url; the storage provider must
	// front it with a public CDN/object domain.
	PublicReadURL(key string) string

	// Upload writes content to the bucket/Key with the given content type.
	Upload(ctx context.Context, key string, r io.Reader, contentType string, sizeBytes int64) error

	// Download streams an object back (the reader must be closed).
	Download(ctx context.Context, key string) (io.ReadCloser, int64, error)

	// Delete removes an object.
	Delete(ctx context.Context, key string) error
}
