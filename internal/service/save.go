package service

import (
	"context"
	"net/http"
	"strings"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/daggix02-m/event_nu/backend/internal/validator"
)

const saveFolderMaxName = 100

// saveStore is the save-repository surface SaveService depends on.
type saveStore interface {
	ListFolders(ctx context.Context, userID string) ([]*domain.SaveFolder, error)
	CreateFolder(ctx context.Context, userID, name string) (*domain.SaveFolder, error)
	GetFolder(ctx context.Context, id string) (*domain.SaveFolder, error)
	UpdateFolder(ctx context.Context, id, name string) (*domain.SaveFolder, error)
	DeleteFolder(ctx context.Context, id string) error
	SaveEvent(ctx context.Context, userID, eventID string, folderID *string) error
	UnsaveEvent(ctx context.Context, userID, eventID string) error
	State(ctx context.Context, userID, eventID string) (bool, *string, error)
	ListSaves(ctx context.Context, userID string, limit, offset int) ([]*domain.SaveWithEvent, error)
	CountSaves(ctx context.Context, userID string) (int, error)
}

type SaveService struct {
	saves  saveStore
	events eventStore
}

func NewSaveService(saves saveStore, events eventStore) *SaveService {
	return &SaveService{saves: saves, events: events}
}

// ---- folders ---------------------------------------------------------------

func (s *SaveService) ListFolders(ctx context.Context, userID string) ([]*domain.SaveFolder, error) {
	return s.saves.ListFolders(ctx, userID)
}

func (s *SaveService) CreateFolder(ctx context.Context, userID, name string) (*domain.SaveFolder, error) {
	name = strings.TrimSpace(name)
	v := validator.New()
	v.Required(name, "name")
	v.MaxChars(name, saveFolderMaxName, "name")
	if !v.Valid() {
		return nil, shared.NewAppError("validation_error", firstFieldError(v), http.StatusUnprocessableEntity)
	}
	return s.saves.CreateFolder(ctx, userID, name)
}

// requireFolder resolves a folder by id and asserts it belongs to the caller.
func (s *SaveService) requireFolder(ctx context.Context, userID, folderID string) (*domain.SaveFolder, error) {
	f, err := s.saves.GetFolder(ctx, folderID)
	if err != nil {
		return nil, err
	}
	if f.UserID != userID {
		return nil, shared.NewAppError("forbidden", "You can only use your own save folders.", http.StatusForbidden)
	}
	return f, nil
}

func (s *SaveService) UpdateFolder(ctx context.Context, userID, folderID, name string) (*domain.SaveFolder, error) {
	name = strings.TrimSpace(name)
	v := validator.New()
	v.Required(name, "name")
	v.MaxChars(name, saveFolderMaxName, "name")
	if !v.Valid() {
		return nil, shared.NewAppError("validation_error", firstFieldError(v), http.StatusUnprocessableEntity)
	}
	if _, err := s.requireFolder(ctx, userID, folderID); err != nil {
		return nil, err
	}
	return s.saves.UpdateFolder(ctx, folderID, name)
}

func (s *SaveService) DeleteFolder(ctx context.Context, userID, folderID string) error {
	if _, err := s.requireFolder(ctx, userID, folderID); err != nil {
		return err
	}
	// Folder deletion is idempotent at the row level; saves in the folder stay
	// and are re-homed to "no folder" by the ON DELETE SET NULL FK.
	return s.saves.DeleteFolder(ctx, folderID)
}

// ---- event saves -----------------------------------------------------------

// SaveEvent saves a publicly visible event, optionally into a folder the caller
// owns. The upsert makes re-saving idempotent. Because save_folders is a
// strict own-row RLS domain, another user's folder id is invisible and surfaces
// as 404 (the same contract as saving into a non-existent folder).
func (s *SaveService) SaveEvent(ctx context.Context, userID, eventID string, folderID *string) error {
	if _, err := s.events.GetByID(ctx, eventID); err != nil {
		return err
	}
	if folderID != nil && *folderID != "" {
		if _, err := s.saves.GetFolder(ctx, *folderID); err != nil {
			return err
		}
	} else {
		folderID = nil
	}
	return s.saves.SaveEvent(ctx, userID, eventID, folderID)
}

// UnsaveEvent is idempotent and ignores event visibility (an event may be
// unsaved even after it is cancelled/archived). Same for folder errors — none.
func (s *SaveService) UnsaveEvent(ctx context.Context, userID, eventID string) error {
	return s.saves.UnsaveEvent(ctx, userID, eventID)
}

func (s *SaveService) State(ctx context.Context, userID, eventID string) (bool, *string, error) {
	return s.saves.State(ctx, userID, eventID)
}

func (s *SaveService) ListSaves(ctx context.Context, userID string, page, limit int) (PageResult[*domain.SaveWithEvent], error) {
	offset := (page - 1) * limit
	items, err := s.saves.ListSaves(ctx, userID, limit, offset)
	if err != nil {
		return PageResult[*domain.SaveWithEvent]{}, err
	}
	total, err := s.saves.CountSaves(ctx, userID)
	if err != nil {
		return PageResult[*domain.SaveWithEvent]{}, err
	}
	return PageResult[*domain.SaveWithEvent]{Items: items, Total: total}, nil
}
