package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MediaRepository struct {
	pool *pgxpool.Pool
}

func NewMediaRepository(pool *pgxpool.Pool) *MediaRepository {
	return &MediaRepository{pool: pool}
}

func (r *MediaRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

const mediaAssetColumns = `id, uploader_id, kind, storage_bucket, storage_key, content_type, byte_size,
	width, height, status, error_message, deleted_at, created_at, updated_at`

func scanMediaAsset(row pgx.Row) (*domain.MediaAsset, error) {
	var a domain.MediaAsset
	err := row.Scan(&a.ID, &a.UploaderID, &a.Kind, &a.StorageBucket, &a.StorageKey, &a.ContentType,
		&a.ByteSize, &a.Width, &a.Height, &a.Status, &a.ErrorMessage, &a.DeletedAt, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan media asset: %w", err)
	}
	return &a, nil
}

func (r *MediaRepository) CreateAsset(ctx context.Context, a *domain.MediaAsset) (*domain.MediaAsset, error) {
	return scanMediaAsset(r.q(ctx).QueryRow(ctx, `
		INSERT INTO media_assets (uploader_id, kind, storage_bucket, storage_key, content_type)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+mediaAssetColumns,
		a.UploaderID, a.Kind, a.StorageBucket, a.StorageKey, a.ContentType))
}

// GetAssetByID returns an asset under RLS (owner, ready+non-deleted public, or
// privileged). A hidden/draft asset with a non-ready status is 404 for a
// non-owner — the ready status IS the reader gate (target schema note).
func (r *MediaRepository) GetAssetByID(ctx context.Context, id string) (*domain.MediaAsset, error) {
	return scanMediaAsset(r.q(ctx).QueryRow(ctx, `
		SELECT `+mediaAssetColumns+` FROM media_assets WHERE id = $1`, id))
}

// SetStatus moves an asset between processing/ready/failed (owner or worker).
func (r *MediaRepository) SetStatus(ctx context.Context, id, status string, errMsg *string) error {
	tag, err := r.q(ctx).Exec(ctx, `
		UPDATE media_assets SET status = $2, error_message = $3, updated_at = now()
		WHERE id = $1`, id, status, errMsg)
	if err != nil {
		return fmt.Errorf("set media status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return shared.ErrNotFound
	}
	return nil
}

// MarkProcessed records the worker's output dimensions and flips status to
// ready (or failed with a message).
func (r *MediaRepository) MarkProcessed(ctx context.Context, id, status string, errMsg *string, width, height int, byteSize int64) error {
	tag, err := r.q(ctx).Exec(ctx, `
		UPDATE media_assets SET status = $2, error_message = $3, width = $4, height = $5, byte_size = $6,
			updated_at = now()
		WHERE id = $1`, id, status, errMsg, width, height, byteSize)
	if err != nil {
		return fmt.Errorf("mark media processed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return shared.ErrNotFound
	}
	return nil
}

// SoftDelete marks an asset deleted (owner or privileged). Ready-public reads
// exclude deleted rows, so the effect is immediate.
func (r *MediaRepository) SoftDelete(ctx context.Context, id string) error {
	tag, err := r.q(ctx).Exec(ctx, `
		UPDATE media_assets SET deleted_at = now(), updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("soft delete media: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return shared.ErrNotFound
	}
	return nil
}

// EnqueueJob inserts a processing job via the SECURITY DEFINER function
// (inserts into the privileged-only job table bypass RLS).
func (r *MediaRepository) EnqueueJob(ctx context.Context, mediaAssetID string) error {
	_, err := r.q(ctx).Exec(ctx, `SELECT enqueue_media_job($1)`, mediaAssetID)
	if err != nil {
		return fmt.Errorf("enqueue media job: %w", err)
	}
	return nil
}

func (r *MediaRepository) CreateVariant(ctx context.Context, v *domain.MediaVariant) error {
	_, err := r.q(ctx).Exec(ctx, `
		INSERT INTO media_variants (media_asset_id, variant, density, format, width, height, storage_key, cdn_url, byte_size)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (media_asset_id, variant, density, format) DO UPDATE
		SET width = EXCLUDED.width, height = EXCLUDED.height, storage_key = EXCLUDED.storage_key,
			cdn_url = EXCLUDED.cdn_url, byte_size = EXCLUDED.byte_size`,
		v.MediaAssetID, v.Variant, v.Density, v.Format, v.Width, v.Height, v.StorageKey, v.CDNURL, v.ByteSize)
	if err != nil {
		return fmt.Errorf("create media variant: %w", err)
	}
	return nil
}

func (r *MediaRepository) ListVariants(ctx context.Context, mediaID string) ([]domain.MediaVariant, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT id, media_asset_id, variant, density, format, width, height, storage_key, cdn_url, byte_size, created_at
		FROM media_variants WHERE media_asset_id = $1
		ORDER BY variant, density`, mediaID)
	if err != nil {
		return nil, fmt.Errorf("list media variants: %w", err)
	}
	defer rows.Close()

	var variants []domain.MediaVariant
	for rows.Next() {
		var v domain.MediaVariant
		if err := rows.Scan(&v.ID, &v.MediaAssetID, &v.Variant, &v.Density, &v.Format, &v.Width, &v.Height,
			&v.StorageKey, &v.CDNURL, &v.ByteSize, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan media variant: %w", err)
		}
		variants = append(variants, v)
	}
	return variants, rows.Err()
}

// ListVariantsForMediaIDs returns the cdn_url of a single variant spec across
// many assets (used to resolve event poster/teaser URLs without per-row
// queries). Missing variants are simply omitted.
func (r *MediaRepository) ListVariantsForMediaIDs(ctx context.Context, mediaIDs []string, variant, density string) (map[string]string, error) {
	if len(mediaIDs) == 0 {
		return map[string]string{}, nil
	}
	rows, err := r.q(ctx).Query(ctx, `
		SELECT media_asset_id, cdn_url
		FROM media_variants
		WHERE media_asset_id = ANY($1) AND variant = $2 AND density = $3`,
		mediaIDs, variant, density)
	if err != nil {
		return nil, fmt.Errorf("list media variants for ids: %w", err)
	}
	defer rows.Close()

	out := make(map[string]string, len(mediaIDs))
	for rows.Next() {
		var id, url string
		if err := rows.Scan(&id, &url); err != nil {
			return nil, fmt.Errorf("scan media variant url: %w", err)
		}
		out[id] = url
	}
	return out, rows.Err()
}

// ClaimJobBatch leases due jobs to one worker, atomically and race-free,
// mirroring the email outbox claim. A claimed job's next_retry_at becomes the
// lease expiry; a crashed worker releases it when the lease lapses. Failed
// jobs with attempts below maxAttempts are re-queued once their backoff delay
// elapses; exhausted jobs are not claimed again.
func (r *MediaRepository) ClaimJobBatch(ctx context.Context, batchSize, maxAttempts int, leaseExpiry string) ([]domain.MediaProcessingJob, error) {
	rows, err := r.q(ctx).Query(ctx, `
		UPDATE media_processing_jobs j
		SET status = 'processing', next_retry_at = now() + $3::interval, updated_at = now()
		FROM media_assets a
		WHERE a.id = j.media_asset_id
			AND j.id IN (
				SELECT j2.id FROM media_processing_jobs j2
				WHERE (j2.status = 'queued'
						OR (j2.status = 'failed' AND j2.attempts < $2 AND j2.next_retry_at <= now()))
					AND j2.next_retry_at <= now()
				ORDER BY j2.updated_at
				FOR UPDATE SKIP LOCKED
				LIMIT $1
			)
		RETURNING j.id, j.media_asset_id, j.status, j.attempts, j.next_retry_at, j.last_error, j.updated_at,
			a.storage_key, a.content_type`,
		batchSize, maxAttempts, leaseExpiry)
	if err != nil {
		return nil, fmt.Errorf("claim media jobs: %w", err)
	}
	defer rows.Close()

	var jobs []domain.MediaProcessingJob
	for rows.Next() {
		var j domain.MediaProcessingJob
		if err := rows.Scan(&j.ID, &j.MediaAssetID, &j.Status, &j.Attempts, &j.NextRetryAt, &j.LastError,
			&j.UpdatedAt, &j.StorageKey, &j.ContentType); err != nil {
			return nil, fmt.Errorf("scan media job: %w", err)
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// MarkJobCompleted finishes a job after its variants are stored.
func (r *MediaRepository) MarkJobCompleted(ctx context.Context, id string) error {
	_, err := r.q(ctx).Exec(ctx, `
		UPDATE media_processing_jobs SET status = 'completed', updated_at = now()
		WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark media job completed: %w", err)
	}
	return nil
}

// MarkJobFailed records an attempt, error, and the next backoff retry.
func (r *MediaRepository) MarkJobFailed(ctx context.Context, id string, attempts int, errMsg string, nextRetryAt interface{}) error {
	_, err := r.q(ctx).Exec(ctx, `
		UPDATE media_processing_jobs SET status = 'failed', attempts = $2, last_error = $3,
			next_retry_at = $4, updated_at = now()
		WHERE id = $1`, id, attempts, errMsg, nextRetryAt)
	if err != nil {
		return fmt.Errorf("mark media job failed: %w", err)
	}
	return nil
}
