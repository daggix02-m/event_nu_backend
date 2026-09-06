package dto

import (
	"encoding/json"
	"testing"
	"time"
)

func dummyTime() time.Time {
	return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
}

func decodeMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, b)
	}
	return m
}

func TestRegisterRequestJSONFieldNames(t *testing.T) {
	req := RegisterRequest{Email: "e@x.co", Password: "secret", Username: "ish"}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	m := decodeMap(t, b)

	for _, k := range []string{"email", "password", "username"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("expected key %q in %s", k, b)
		}
	}
	if m["email"] != "e@x.co" || m["username"] != "ish" {
		t.Fatalf("unexpected values: %v", m)
	}
}

func TestRegisterRequestRoundTrip(t *testing.T) {
	req := RegisterRequest{Email: "e@x.co", Password: "secret", Username: "ish"}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got RegisterRequest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != req {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, req)
	}
}

func TestAuthResponseJSONKeysFollowSpec(t *testing.T) {
	resp := AuthResponse{AccessToken: "at", RefreshToken: "rt", ExpiresIn: 900}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	m := decodeMap(t, b)

	for _, k := range []string{"access_token", "refresh_token", "expires_in_seconds", "user"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("expected key %q in %s", k, b)
		}
	}
	if m["expires_in_seconds"].(float64) != 900 {
		t.Fatalf("unexpected expires_in_seconds: %v", m["expires_in_seconds"])
	}
}

func TestUserDTOJSONKeys(t *testing.T) {
	u := UserDTO{
		ID: "u1", Email: "e@x.co", Username: "ish", Role: "user",
		IsVerified: true, CreatedAt: dummyTime(),
	}
	b, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	m := decodeMap(t, b)

	for _, k := range []string{"id", "email", "username", "role", "is_verified", "created_at"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("expected key %q in %s", k, b)
		}
	}
	if m["is_verified"] != true {
		t.Fatalf("expected is_verified true, got %v", m["is_verified"])
	}
}

func TestApplyOrganizerRequestFieldNames(t *testing.T) {
	req := ApplyOrganizerRequest{RequestedName: "Name", RequestedSlug: "name", Bio: "bio"}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	m := decodeMap(t, b)
	for _, k := range []string{"requested_name", "requested_slug", "bio"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("expected key %q in %s", k, b)
		}
	}
}

func TestCreateEventRequestOptionalPointers(t *testing.T) {
	req := CreateEventRequest{Title: "T", StartsAt: "2026-09-06T10:00:00Z"}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	m := decodeMap(t, b)

	// Optional pointers marshal as JSON null when unset (no omitempty).
	for _, k := range []string{"venue_id", "category_id", "ends_at", "max_attendees"} {
		if got, ok := m[k]; !ok || got != nil {
			t.Fatalf("expected key %q to be present and null, got %v (%t)", k, got, ok)
		}
	}
	if m["title"] != "T" || m["starts_at"] != "2026-09-06T10:00:00Z" {
		t.Fatalf("unexpected values: %v", m)
	}
}

func TestCreateVenueRequestFieldNames(t *testing.T) {
	req := CreateVenueRequest{Name: "H", Address: "A", Latitude: 1.5, Longitude: -2.5}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	m := decodeMap(t, b)
	for _, k := range []string{"name", "address", "latitude", "longitude"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("expected key %q in %s", k, b)
		}
	}
	if m["latitude"].(float64) != 1.5 || m["longitude"].(float64) != -2.5 {
		t.Fatalf("unexpected coordinates: %v", m)
	}
}
