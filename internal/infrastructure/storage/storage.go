// Package storage hosts the object-storage adapters for the media pipeline.
// The contracts (ObjectStore) live in package domain (hexagonal slice 4).
// The local provider stores into a directory and serves objects through the
// API's /media/objects route (dev/tests); the S3 provider uses the
// S3-compatible API (MinIO locally, Cloudflare R2 in production) with
// presigned URLs.
package storage
