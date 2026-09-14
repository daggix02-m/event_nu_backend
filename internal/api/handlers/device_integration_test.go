package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/api/dto"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
)

func cleanupDevices(t *testing.T, userIDs ...string) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return
	}
	pool, err := database.NewPoolWithRole(context.Background(), dsn, "service")
	if err != nil {
		t.Fatalf("service pool: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM user_devices WHERE user_id = ANY($1::uuid[])`, userIDs); err != nil {
		t.Fatalf("cleanup devices: %v", err)
	}
}

func registerDevice(t *testing.T, h http.Handler, token, platform, userToken string) (string, string) {
	t.Helper()
	body := fmt.Sprintf(`{"token":%q,"platform":%q}`, token, platform)
	rec := doJSON(t, h, http.MethodPost, "/api/v1/devices", body, userToken)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register device: %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data dto.DeviceDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode device: %v", err)
	}
	if resp.Data.Token != token {
		t.Fatalf("token echo mismatch: got %q want %q", resp.Data.Token, token)
	}
	return resp.Data.ID, resp.Data.Token
}

func listDevices(t *testing.T, h http.Handler, userToken string) []dto.DeviceDTO {
	t.Helper()
	rec := doJSON(t, h, http.MethodGet, "/api/v1/me/devices", "", userToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("list devices: %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []dto.DeviceDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return resp.Data
}

// TestDeviceRegistryLifecycle covers register → list → deregister through the
// public API, including idempotent re-registration of the same token and the
// RLS backstop that a second user cannot see or remove the first user's row.
func TestDeviceRegistryLifecycle(t *testing.T) {
	app, h := newTestApp(t)
	if app == nil {
		return
	}

	_, tok1, uid1 := registerAndToken(t, h, "devapi")
	_, tok2, uid2 := registerAndToken(t, h, "devapi2")
	cleanupDevices(t, uid1, uid2)

	devToken := fmt.Sprintf("fcm-api-%d", time.Now().UnixNano())

	// — register, then re-register the same token (upsert, single row) —
	id1, _ := registerDevice(t, h, devToken, "android", tok1)
	id2, _ := registerDevice(t, h, devToken, "ios", tok2)
	if id1 != id2 {
		t.Fatalf("re-registration must reuse the row: %q vs %q", id1, id2)
	}

	// — user 2 owns it now; user 1 sees nothing —
	list2 := listDevices(t, h, tok2)
	if len(list2) != 1 || list2[0].Platform != "ios" {
		t.Fatalf("user2 must own 1 ios device, got %+v", list2)
	}
	if list1 := listDevices(t, h, tok1); len(list1) != 0 {
		t.Fatalf("user1 must see 0 devices after handover, got %d", len(list1))
	}

	// — user1 cannot delete user2's device —
	if rec := doJSON(t, h, http.MethodDelete, "/api/v1/devices/"+id2, "", tok1); rec.Code != http.StatusOK {
		t.Fatalf("cross-user delete: %d: %s", rec.Code, rec.Body.String())
	}
	if list2 := listDevices(t, h, tok2); len(list2) != 1 {
		t.Fatalf("user2's device must survive user1's delete, got %d", len(list2))
	}

	// — user2 deregisters their own device —
	if rec := doJSON(t, h, http.MethodDelete, "/api/v1/devices/"+id2, "", tok2); rec.Code != http.StatusOK {
		t.Fatalf("deregister: %d: %s", rec.Code, rec.Body.String())
	}
	if list2 := listDevices(t, h, tok2); len(list2) != 0 {
		t.Fatalf("user2's device must be gone, got %d", len(list2))
	}
}

// TestDeviceRegistryValidationErrors covers auth + input validation.
func TestDeviceRegistryValidationErrors(t *testing.T) {
	_, h := newTestApp(t)

	// — requires auth —
	if rec := doJSON(t, h, http.MethodPost, "/api/v1/devices", `{"token":"x"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("register without token: %d", rec.Code)
	}

	// — empty token is invalid —
	_, userTok, uid := registerAndToken(t, h, "devval")
	cleanupDevices(t, uid)
	if rec := doJSON(t, h, http.MethodPost, "/api/v1/devices", `{"token":""}`, userTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty token: %d: %s", rec.Code, rec.Body.String())
	}

	// — deregister with a malformed id is 404 —
	if rec := doJSON(t, h, http.MethodDelete, "/api/v1/devices/not-a-uuid", "", userTok); rec.Code != http.StatusNotFound {
		t.Fatalf("malformed device id: %d", rec.Code)
	}
}

// TestDeviceRegistryValidationBadJSON ensures a malformed body is rejected,
// not silently ignored by the idempotent register endpoint.
func TestDeviceRegistryValidationBadJSON(t *testing.T) {
	_, h := newTestApp(t)
	_, userTok, uid := registerAndToken(t, h, "devbad")
	cleanupDevices(t, uid)
	if rec := doJSON(t, h, http.MethodPost, "/api/v1/devices", `{`, userTok); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed body: %d", rec.Code)
	}
}
