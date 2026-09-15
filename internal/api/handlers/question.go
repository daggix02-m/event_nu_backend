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

type QuestionHandlers struct {
	app      *api.Application
	question *service.QuestionService
}

func NewQuestionHandlers(app *api.Application, question *service.QuestionService) *QuestionHandlers {
	return &QuestionHandlers{app: app, question: question}
}

// Create posts a question on a published event (any signed-in user).
func (h *QuestionHandlers) Create(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	var req dto.CreateQuestionRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	q, err := h.question.Create(r.Context(), middleware.UserID(r.Context()), eventID, req.Body)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, toQuestionDTO(q))
}

// List returns paginated questions for a published event, pinned first then by
// vote count. Runs under optional auth so the viewer's voted_by_me is accurate
// for signed-in callers and false for anonymous ones.
func (h *QuestionHandlers) List(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	result, err := h.question.List(r.Context(), eventID, middleware.UserID(r.Context()), page, limit)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.QuestionDTO, 0, len(result.Items))
	for _, item := range result.Items {
		out = append(out, toQuestionWithVoteDTO(item))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   result.Total,
		HasNext: page*limit < result.Total,
	})
}

// Upvote adds the caller's upvote to a question. Idempotent: a duplicate vote
// is a no-op thanks to the (question_id, user_id) primary key.
func (h *QuestionHandlers) Upvote(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if err := h.question.Upvote(r.Context(), middleware.UserID(r.Context()), id); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "voted"})
}

// RemoveUpvote removes the caller's upvote. Idempotent.
func (h *QuestionHandlers) RemoveUpvote(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if err := h.question.RemoveUpvote(r.Context(), middleware.UserID(r.Context()), id); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "unvoted"})
}

// Pin pins/unpins a question (event organizer only). Accepts an optional body;
// when pinned is absent the current state is toggled.
func (h *QuestionHandlers) Pin(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	pinned := true
	var req dto.PinRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Pinned != nil {
		pinned = *req.Pinned
	}
	if err := h.question.Pin(r.Context(), middleware.UserID(r.Context()), id, pinned); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]any{"status": "updated", "pinned": pinned})
}

// Answer sets (or clears with an empty body) the organizer's answer.
func (h *QuestionHandlers) Answer(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	var req dto.AnswerRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := h.question.Answer(r.Context(), middleware.UserID(r.Context()), id, req.Answer); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "answered"})
}

func toQuestionDTO(q *domain.Question) dto.QuestionDTO {
	return dto.QuestionDTO{
		ID:        q.ID,
		EventID:   q.EventID,
		UserID:    q.UserID,
		Body:      q.Body,
		Pinned:    q.Pinned,
		Answer:    q.Answer,
		Votes:     0,
		VotedByMe: false,
		CreatedAt: q.CreatedAt,
		UpdatedAt: q.UpdatedAt,
	}
}

func toQuestionWithVoteDTO(q *domain.QuestionWithVote) dto.QuestionDTO {
	return dto.QuestionDTO{
		ID:        q.ID,
		EventID:   q.EventID,
		UserID:    q.UserID,
		Body:      q.Body,
		Pinned:    q.Pinned,
		Answer:    q.Answer,
		Votes:     q.Votes,
		VotedByMe: q.VotedByMe,
		CreatedAt: q.CreatedAt,
		UpdatedAt: q.UpdatedAt,
	}
}
