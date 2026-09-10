package worker

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/imaging"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/storage"
	"github.com/daggix02-m/event_nu/backend/internal/repository"
	"github.com/daggix02-m/event_nu/backend/internal/service"
)

// mediaJobStore is the persistence surface the media consumer needs (mirrors
// emailOutboxStore so reliability tests can inject faulting stores).
type mediaJobStore interface {
	ClaimJobBatch(ctx context.Context, batchSize, maxAttempts int, leaseExpiry string) ([]domain.MediaProcessingJob, error)
	MarkJobCompleted(ctx context.Context, id string) error
	MarkJobFailed(ctx context.Context, id string, attempts int, errMsg string, nextRetryAt interface{}) error
	CreateVariant(ctx context.Context, v *domain.MediaVariant) error
	MarkProcessed(ctx context.Context, id, status string, errMsg *string, width, height int, byteSize int64) error
}

// MediaConsumer drains media_processing_jobs: it downloads the original,
// produces the variant set, stores every variant, and flips the asset to
// ready. Failures are retried with exponential backoff and dead-lettered after
// MediaJobMaxAttempts (the asset then reports status=failed).
type MediaConsumer struct {
	logger *slog.Logger
	cfg    config.Config
	jobs   mediaJobStore
	store  storage.ObjectStore
}

func NewMediaConsumer(logger *slog.Logger, cfg config.Config, jobs *repository.MediaRepository, store storage.ObjectStore) *MediaConsumer {
	return &MediaConsumer{logger: logger, cfg: cfg, jobs: jobs, store: store}
}

func (c *MediaConsumer) Run(ctx context.Context) error {
	c.logger.Info("media consumer started",
		"poll_interval", c.cfg.MediaJobPollInterval.String(),
		"batch_size", c.cfg.MediaJobBatchSize,
		"max_attempts", c.cfg.MediaJobMaxAttempts)
	ticker := time.NewTicker(c.cfg.MediaJobPollInterval)
	defer ticker.Stop()

	for {
		if err := c.processBatch(ctx); err != nil && ctx.Err() == nil {
			c.logger.Error("media batch failed", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			c.logger.Info("media consumer stopped")
			return nil
		case <-ticker.C:
		}
	}
}

func (c *MediaConsumer) processBatch(ctx context.Context) error {
	jobs, err := c.jobs.ClaimJobBatch(ctx, c.cfg.MediaJobBatchSize, c.cfg.MediaJobMaxAttempts, "5m")
	if err != nil {
		return err
	}
	c.logger.Debug("claimed media batch", "count", len(jobs))
	for _, job := range jobs {
		if err := c.processOne(ctx, job); err != nil && ctx.Err() == nil {
			c.logger.Error("media job failed", "job_id", job.ID, "asset_id", job.MediaAssetID, "error", err.Error())
		}
	}
	return nil
}

func (c *MediaConsumer) processOne(ctx context.Context, job domain.MediaProcessingJob) error {
	attempt := job.Attempts + 1

	rc, size, err := c.store.Download(ctx, job.StorageKey)
	if err != nil {
		return c.recordFailure(ctx, job, attempt, err)
	}
	defer rc.Close()

	result, err := imaging.Process(rc, job.ContentType)
	if err != nil {
		return c.recordFailure(ctx, job, attempt, err)
	}

	for _, v := range result.Variants {
		format := v.Format
		density := fmt.Sprintf("%dx", v.Density)
		key := service.VariantKey(job.StorageKey, v.Spec, density, format)
		contentType := "image/webp"
		if format == imaging.FormatJPEG {
			contentType = "image/jpeg"
		}
		if err := c.store.Upload(ctx, key, bytes.NewReader(v.Data), contentType, int64(len(v.Data))); err != nil {
			return c.recordFailure(ctx, job, attempt, fmt.Errorf("store variant %s: %w", key, err))
		}
		byteSize := int64(len(v.Data))
		if err := c.jobs.CreateVariant(ctx, &domain.MediaVariant{
			MediaAssetID: job.MediaAssetID,
			Variant:      v.Spec,
			Density:      density,
			Format:       format,
			Width:        v.Width,
			Height:       v.Height,
			StorageKey:   key,
			CDNURL:       c.store.PublicReadURL(key),
			ByteSize:     &byteSize,
		}); err != nil {
			return c.recordFailure(ctx, job, attempt, fmt.Errorf("record variant %s: %w", key, err))
		}
	}

	if err := c.jobs.MarkProcessed(ctx, job.MediaAssetID, domain.MediaStatusReady, nil,
		result.Width, result.Height, size); err != nil {
		return err
	}
	if err := c.jobs.MarkJobCompleted(ctx, job.ID); err != nil {
		return err
	}
	c.logger.Info("media asset processed", "asset_id", job.MediaAssetID, "variants", len(result.Variants))
	return nil
}

func (c *MediaConsumer) recordFailure(ctx context.Context, job domain.MediaProcessingJob, attempt int, err error) error {
	if attempt >= c.cfg.MediaJobMaxAttempts {
		msg := err.Error()
		// Dead-letter: asset reports failed; job stops being claimed (claim
		// requires attempts < maxAttempts).
		_ = c.jobs.MarkProcessed(ctx, job.MediaAssetID, domain.MediaStatusFailed, &msg, 0, 0, 0)
		_ = c.jobs.MarkJobFailed(ctx, job.ID, attempt, msg, time.Now().Add(time.Hour))
		c.logger.Error("media asset dead-lettered", "asset_id", job.MediaAssetID, "attempt", attempt, "error", msg)
		return nil
	}
	next := time.Now().Add(c.cfg.MediaJobRetryBaseDelay * time.Duration(1<<min(attempt-1, 6)))
	c.logger.Warn("media job retry scheduled", "job_id", job.ID, "asset_id", job.MediaAssetID, "attempt", attempt, "retry_at", next.String())
	return c.jobs.MarkJobFailed(ctx, job.ID, attempt, err.Error(), next)
}
