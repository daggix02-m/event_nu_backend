package storage

import (
	"fmt"
	"strings"
)

// New builds the configured ObjectStore. provider is "local" (filesystem; the
// only provider usable in hermetic tests) or "s3" (S3-compatible API — MinIO
// locally, Cloudflare R2 in production). An empty provider falls back to
// "local", the development default.
func New(provider, endpoint, region, bucket, accessKey, secretKey, publicBase, localDir string) (ObjectStore, error) {
	if provider == "" {
		provider = "local"
	}
	switch strings.ToLower(provider) {
	case "local":
		return NewLocal(localDir, publicBase), nil
	case "s3":
		return NewS3(endpoint, region, bucket, accessKey, secretKey, publicBase), nil
	default:
		return nil, fmt.Errorf("unknown storage provider %q", provider)
	}
}
