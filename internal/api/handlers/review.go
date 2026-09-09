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

type ReviewHandlers struct {
	app     *api.Application
	reviews *service.ReviewService
}

func NewReviewHandlers(app *api.Application, reviews *service.ReviewService) *ReviewHandlers {
	return &ReviewHandlers{app: app, reviews: reviews}
}

func (h *ReviewHandlers) List(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	result, err := h.reviews.List(r.Context(), eventID, page, limit)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.ReviewDTO, 0, len(result.Items))
	for _, rev := range result.Items {
		out = append(out, toReviewDTO(rev))
	}
	writePaginated(w, http.StatusOK, out, dto.PaginationMeta{
		Page:    page,
		Limit:   limit,
		Total:   result.Total,
		HasNext: page*limit < result.Total,
	})
}

func (h *ReviewHandlers) Create(w http.ResponseWriter, r *http.Request) {
	eventID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	var req dto.ReviewRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	rev, err := h.reviews.Create(r.Context(), middleware.UserID(r.Context()), eventID, int16(req.Rating), req.Body)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, toReviewDTO(rev))
}

func (h *ReviewHandlers) Update(w http.ResponseWriter, r *http.Request) {
	reviewID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	var req dto.ReviewRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	rev, err := h.reviews.Update(r.Context(), middleware.UserID(r.Context()), reviewID, int16(req.Rating), req.Body)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, toReviewDTO(rev))
}

func (h *ReviewHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	reviewID, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	userID := middleware.UserID(r.Context())
	admin := false
	if u, err := h.app.Auth.GetUser(r.Context(), userID); err == nil && string(u.Role) == "admin" {
		admin = true
	}
	if err := h.reviews.Delete(r.Context(), userID, reviewID, admin); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func toReviewDTO(rev *domain.Review) dto.ReviewDTO {
	return dto.ReviewDTO{
		ID:        rev.ID,
		EventID:   rev.EventID,
		UserID:    rev.UserID,
		Rating:    rev.Rating,
		Body:      rev.Body,
		Status:    rev.Status,
		CreatedAt: rev.CreatedAt,
		UpdatedAt: rev.UpdatedAt,
	}
}
