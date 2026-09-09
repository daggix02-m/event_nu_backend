package handlers

import (
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
	"github.com/daggix02-m/event_nu/backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/service"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

type SaveHandlers struct {
	app   *api.Application
	saves *service.SaveService
}

func NewSaveHandlers(app *api.Application, saves *service.SaveService) *SaveHandlers {
	return &SaveHandlers{app: app, saves: saves}
}

func (h *SaveHandlers) ListFolders(w http.ResponseWriter, r *http.Request) {
	folders, err := h.saves.ListFolders(r.Context(), middleware.UserID(r.Context()))
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.SaveFolderDTO, 0, len(folders))
	for _, f := range folders {
		out = append(out, toSaveFolderDTO(f))
	}
	shared.WriteJSON(w, http.StatusOK, out)
}

func (h *SaveHandlers) CreateFolder(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateSaveFolderRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	folder, err := h.saves.CreateFolder(r.Context(), middleware.UserID(r.Context()), req.Name)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, toSaveFolderDTO(folder))
}

func (h *SaveHandlers) UpdateFolder(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	var req dto.UpdateSaveFolderRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	folder, err := h.saves.UpdateFolder(r.Context(), middleware.UserID(r.Context()), id, req.Name)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, toSaveFolderDTO(folder))
}

func (h *SaveHandlers) DeleteFolder(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if err := h.saves.DeleteFolder(r.Context(), middleware.UserID(r.Context()), id); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *SaveHandlers) SaveEvent(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	var req dto.SaveEventRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := h.saves.SaveEvent(r.Context(), middleware.UserID(r.Context()), eventID, req.FolderID); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	h.writeSaveState(w, r, eventID)
}

func (h *SaveHandlers) UnsaveEvent(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if err := h.saves.UnsaveEvent(r.Context(), middleware.UserID(r.Context()), eventID); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	h.writeSaveState(w, r, eventID)
}

func (h *SaveHandlers) ListSaves(w http.ResponseWriter, r *http.Request) {
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	result, err := h.saves.ListSaves(r.Context(), middleware.UserID(r.Context()), page, limit)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.SaveDTO, 0, len(result.Items))
	for _, s := range result.Items {
		out = append(out, toSaveDTO(s))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   result.Total,
		HasNext: page*limit < result.Total,
	})
}

func (h *SaveHandlers) writeSaveState(w http.ResponseWriter, r *http.Request, eventID string) {
	saved, folderID, err := h.saves.State(r.Context(), middleware.UserID(r.Context()), eventID)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, dto.SaveState{
		Saved:    saved,
		FolderID: folderID,
	})
}

func toSaveFolderDTO(f *domain.SaveFolder) dto.SaveFolderDTO {
	return dto.SaveFolderDTO{
		ID:        f.ID,
		Name:      f.Name,
		CreatedAt: f.CreatedAt,
		UpdatedAt: f.UpdatedAt,
	}
}

func toSaveDTO(s *domain.SaveWithEvent) dto.SaveDTO {
	out := dto.SaveDTO{
		EventID:   s.EventID,
		FolderID:  s.FolderID,
		CreatedAt: s.CreatedAt,
	}
	if s.EventTime != nil {
		visible := s.EventTitle != ""
		out.Event = &dto.EventSummary{
			ID:        s.EventID,
			Title:     s.EventTitle,
			StartsAt:  *s.EventTime,
			Status:    "published",
			IsVisible: visible,
		}
	}
	return out
}
