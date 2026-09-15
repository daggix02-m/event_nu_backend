package handlers

import (
	"net/http"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
	"github.com/daggix02-m/event_nu/backend/internal/api/middleware"
	"github.com/daggix02-m/event_nu/backend/internal/service"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

type BadgeHandlers struct {
	app   *api.Application
	badge *service.BadgeService
}

func NewBadgeHandlers(app *api.Application, badge *service.BadgeService) *BadgeHandlers {
	return &BadgeHandlers{app: app, badge: badge}
}

// MyBadges returns the caller's earned milestone badges, newest first.
func (h *BadgeHandlers) MyBadges(w http.ResponseWriter, r *http.Request) {
	badges, err := h.badge.MyBadges(r.Context(), middleware.UserID(r.Context()))
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.BadgeDTO, 0, len(badges))
	for _, b := range badges {
		out = append(out, dto.BadgeDTO{
			ID:        b.ID,
			BadgeType: b.BadgeType,
			EarnedAt:  b.EarnedAt,
			Metadata:  b.Metadata,
		})
	}
	shared.WriteJSON(w, http.StatusOK, out)
}
