package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SaveRepository manages save folders and event saves. Both tables are
// strict own-row RLS domains: every query is naturally scoped to the
// authenticated user via current_app_user_id().
type SaveRepository struct {
	pool *pgxpool.Pool
}

func NewSaveRepository(pool *pgxpool.Pool) *SaveRepository {
	return &SaveRepository{pool: pool}
}

func (r *SaveRepository) q(ctx context.Context) database.Querier {
	return database.QuerierFromContext(ctx, r.pool)
}

// ---- folders ---------------------------------------------------------------

const saveFolderColumns = "id, user_id, name, created_at, updated_at"

func scanSaveFolder(row pgx.Row) (*domain.SaveFolder, error) {
	var f domain.SaveFolder
	err := row.Scan(&f.ID, &f.UserID, &f.Name, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan save folder: %w", err)
	}
	return &f, nil
}

func (r *SaveRepository) ListFolders(ctx context.Context, userID string) ([]*domain.SaveFolder, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+saveFolderColumns+`
		FROM save_folders
		WHERE user_id = $1
		ORDER BY created_at ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list save folders: %w", err)
	}
	defer rows.Close()

	var out []*domain.SaveFolder
	for rows.Next() {
		f, err := scanSaveFolder(rows)
		if err != nil {
			return nil, fmt.Errorf("scan save folder row: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate save folders: %w", err)
	}
	return out, nil
}

func (r *SaveRepository) CreateFolder(ctx context.Context, userID, name string) (*domain.SaveFolder, error) {
	row := r.q(ctx).QueryRow(ctx, `
		INSERT INTO save_folders (user_id, name)
		VALUES ($1, $2)
		RETURNING `+saveFolderColumns, userID, name)
	return scanSaveFolder(row)
}

func (r *SaveRepository) GetFolder(ctx context.Context, id string) (*domain.SaveFolder, error) {
	row := r.q(ctx).QueryRow(ctx, `
		SELECT `+saveFolderColumns+`
		FROM save_folders
		WHERE id = $1`, id)
	return scanSaveFolder(row)
}

func (r *SaveRepository) UpdateFolder(ctx context.Context, id, name string) (*domain.SaveFolder, error) {
	row := r.q(ctx).QueryRow(ctx, `
		UPDATE save_folders
		SET name = $2, updated_at = now()
		WHERE id = $1
		RETURNING `+saveFolderColumns, id, name)
	return scanSaveFolder(row)
}

func (r *SaveRepository) DeleteFolder(ctx context.Context, id string) error {
	_, err := r.q(ctx).Exec(ctx, `DELETE FROM save_folders WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete save folder: %w", err)
	}
	return nil
}

// ---- saves ------------------------------------------------------------------

// SaveEvent upserts a save (idempotent under double-tap); ON CONFLICT updates
// the folder so re-saving into a different folder moves the save.
func (r *SaveRepository) SaveEvent(ctx context.Context, userID, eventID string, folderID *string) error {
	_, err := r.q(ctx).Exec(ctx, `
		INSERT INTO saves (user_id, event_id, folder_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, event_id) DO UPDATE SET folder_id = EXCLUDED.folder_id,
		                                                created_at = saves.created_at`,
		userID, eventID, folderID)
	if err != nil {
		return fmt.Errorf("save event: %w", err)
	}
	return nil
}

func (r *SaveRepository) UnsaveEvent(ctx context.Context, userID, eventID string) error {
	_, err := r.q(ctx).Exec(ctx, `
		DELETE FROM saves
		WHERE user_id = $1 AND event_id = $2`, userID, eventID)
	if err != nil {
		return fmt.Errorf("unsave event: %w", err)
	}
	return nil
}

// State reports whether the user saved the event and into which folder.
func (r *SaveRepository) State(ctx context.Context, userID, eventID string) (bool, *string, error) {
	var folderID *string
	// NULLIF guards anonymous reads (userID == "") so the ::uuid cast sees NULL
	// instead of erroring, mirroring LikeRepository.State.
	err := r.q(ctx).QueryRow(ctx, `
		SELECT folder_id
		FROM saves
		WHERE user_id = NULLIF($1, '')::uuid AND event_id = $2`, userID, eventID).Scan(&folderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("save state: %w", err)
	}
	return true, folderID, nil
}

// ListSaves returns the caller's saves (own-row RLS) with a LEFT JOIN onto
// events. Event columns are COALESCE'd to empty/new values when the event is
// no longer visible under RLS, so a saved-but-hidden event surfaces as a
// notification of its (former) existence rather than vanishing from the list.
func (r *SaveRepository) ListSaves(ctx context.Context, userID string, limit, offset int) ([]*domain.SaveWithEvent, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT s.user_id, s.event_id, s.folder_id, s.created_at,
		       COALESCE(e.title, ''),
		       COALESCE(e.starts_at, to_timestamp(0)) AS event_starts_at
		FROM saves s
		LEFT JOIN events e ON e.id = s.event_id
		WHERE s.user_id = $1
		ORDER BY s.created_at DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list saves: %w", err)
	}
	defer rows.Close()

	var out []*domain.SaveWithEvent
	for rows.Next() {
		var s domain.SaveWithEvent
		var hidden bool
		if err := rows.Scan(&s.UserID, &s.EventID, &s.FolderID, &s.CreatedAt, &s.EventTitle, &s.EventStart); err != nil {
			return nil, fmt.Errorf("scan save row: %w", err)
		}
		hidden = s.EventStart.Equal(time.Unix(0, 0))
		if !hidden {
			s.EventTime = &s.EventStart
		}
		out = append(out, &s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate saves: %w", err)
	}
	return out, nil
}

func (r *SaveRepository) CountSaves(ctx context.Context, userID string) (int, error) {
	var n int
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) FROM saves WHERE user_id = $1`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count saves: %w", err)
	}
	return n, nil
}
