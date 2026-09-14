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

type DeviceHandlers struct {
	app     *api.Application
	devices *service.DeviceService
}

func NewDeviceHandlers(app *api.Application, devices *service.DeviceService) *DeviceHandlers {
	return &DeviceHandlers{app: app, devices: devices}
}

// Register upserts the caller's device token for push delivery.
func (h *DeviceHandlers) Register(w http.ResponseWriter, r *http.Request) {
	var req dto.DeviceRequest
	if err := shared.DecodeJSON(w, r, &req); err != nil {
		h.app.ClientError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	device, err := h.devices.Register(r.Context(), middleware.UserID(r.Context()), req.Token, req.Platform)
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, toDeviceDTO(device))
}

// Deregister removes the caller's device; idempotent.
func (h *DeviceHandlers) Deregister(w http.ResponseWriter, r *http.Request) {
	id, ok := ParseUUIDParam(r, "id")
	if !ok {
		h.app.NotFound(w, r)
		return
	}
	if err := h.devices.Deregister(r.Context(), middleware.UserID(r.Context()), id); err != nil {
		h.app.AppError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, dto.DeviceDeleteResult{Deleted: true})
}

// List returns the caller's registered devices.
func (h *DeviceHandlers) List(w http.ResponseWriter, r *http.Request) {
	devices, err := h.devices.List(r.Context(), middleware.UserID(r.Context()))
	if err != nil {
		h.app.AppError(w, r, err)
		return
	}
	out := make([]dto.DeviceDTO, 0, len(devices))
	for _, d := range devices {
		out = append(out, toDeviceDTO(d))
	}
	shared.WriteJSON(w, http.StatusOK, out)
}

func toDeviceDTO(d *domain.Device) dto.DeviceDTO {
	return dto.DeviceDTO{
		ID:         d.ID,
		Token:      d.Token,
		Platform:   d.Platform,
		LastSeenAt: d.LastSeenAt,
		CreatedAt:  d.CreatedAt,
	}
}
