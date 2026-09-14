package service

import (
	"context"
	"net/http"
	"strings"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

// maxTokenLength bounds the FCM registration token (FCM tokens are ~250 chars;
// 4096 leaves headroom without inviting multi-KB rows).
const maxTokenLength = 4096

// deviceStore is the device-repository surface DeviceService needs.
type deviceStore interface {
	Upsert(ctx context.Context, userID, token, platform string) (*domain.Device, error)
	Delete(ctx context.Context, userID, id string) error
	ListByUser(ctx context.Context, userID string) ([]*domain.Device, error)
}

// DeviceService owns the push-device registry: register/deregister under the
// caller's identity (RLS guarantees own-row access) and a read for the inbox.
type DeviceService struct {
	devices deviceStore
}

func NewDeviceService(devices deviceStore) *DeviceService {
	return &DeviceService{devices: devices}
}

// Register upserts the caller's device token (idempotent per device). An
// unknown platform string falls back to "android".
func (s *DeviceService) Register(ctx context.Context, userID, token, platform string) (*domain.Device, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, shared.NewAppError("validation_error", "Device token is required.", http.StatusUnprocessableEntity)
	}
	if len(token) > maxTokenLength {
		return nil, shared.NewAppError("validation_error", "Device token is too long.", http.StatusUnprocessableEntity)
	}
	switch platform {
	case "android", "ios", "web":
	case "":
		platform = "android"
	default:
		platform = "android"
	}
	return s.devices.Upsert(ctx, userID, token, platform)
}

// Deregister removes the caller's device; idempotent.
func (s *DeviceService) Deregister(ctx context.Context, userID, id string) error {
	return s.devices.Delete(ctx, userID, id)
}

// List returns the caller's registered devices.
func (s *DeviceService) List(ctx context.Context, userID string) ([]*domain.Device, error) {
	return s.devices.ListByUser(ctx, userID)
}
