// Package storage abstracts object storage for the media pipeline. The local
// provider stores into a directory and serves objects through the API's
// /media/objects route (dev/tests); the S3 provider uses the S3-compatible API
// (MinIO locally, Cloudflare R2 in production) with presigned URLs.
package storage

import (
	"context"
	"io"
	"time"
)

// ObjectStore is the storage surface the media service and worker need.
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
