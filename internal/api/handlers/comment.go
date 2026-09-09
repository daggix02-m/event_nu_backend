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

type CommentHandlers struct {
	app      *api.Application
	comments *service.CommentService
}

func NewCommentHandlers(app *api.Application, comments *service.CommentService) *CommentHandlers {
	return &CommentHandlers{app: app, comments: comments}
}

func (h *CommentHandlers) List(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	result, err := h.comments.List(r.Context(), eventID, page, limit)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.CommentDTO, 0, len(result.Items))
	for _, c := range result.Items {
		out = append(out, toCommentDTO(c))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   result.Total,
		HasNext: page*limit < result.Total,
	})
}

func (h *CommentHandlers) Create(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	var req dto.CreateCommentRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	comment, err := h.comments.Create(r.Context(), middleware.UserID(r.Context()), eventID, req.Body)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, toCommentDTO(comment))
}

func (h *CommentHandlers) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	var req dto.UpdateCommentRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	comment, err := h.comments.Update(r.Context(), middleware.UserID(r.Context()), id, req.Body)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, toCommentDTO(comment))
}

// Delete removes a comment. Own comments are deletable by anyone; an admin may
// delete any comment (moderation path).
func (h *CommentHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	userID := middleware.UserID(r.Context())
	admin := false
	if u, err := h.app.Auth.GetUser(r.Context(), userID); err == nil && string(u.Role) == "admin" {
		admin = true
	}
	if err := h.comments.Delete(r.Context(), userID, id, admin); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func toCommentDTO(c *domain.Comment) dto.CommentDTO {
	return dto.CommentDTO{
		ID:               c.ID,
		EventID:          c.EventID,
		UserID:           c.UserID,
		Body:             c.Body,
		ModerationStatus: c.ModerationStatus,
		CreatedAt:        c.CreatedAt,
		UpdatedAt:        c.UpdatedAt,
	}
}
