package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type prefsResponse struct {
	Data struct {
		CategoryIDs         []string  `json:"category_ids"`
		UpdatedAt           time.Time `json:"updated_at"`
		OnboardingCompleted bool      `json:"onboarding_completed"`
	} `json:"data"`
}

func decodePrefs(t *testing.T, rec *httptest.ResponseRecorder) prefsResponse {
	t.Helper()
	var resp prefsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode prefs response: %v; body=%s", err, rec.Body.String())
	}
	return resp
}

func activeCategoryID(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := doJSON(t, h, http.MethodGet, "/api/v1/categories", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list categories: expected 200, got %d", rec.Code)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode categories: %v", err)
	}
	if len(body.Data) == 0 {
		t.Fatal("expected at least one category")
	}
	return body.Data[0].ID
}

func TestOnboardingPreferencesFlow(t *testing.T) {
	_, h := newTestApp(t)
	email := fmt.Sprintf("prefs+%d@test.example", time.Now().UnixNano())
	reg := registerUser(t, h, email)
	token := reg.Data.AccessToken
	catID := activeCategoryID(t, h)

	// A freshly registered user has not completed onboarding.
	if reg.Data.User.OnboardingCompleted {
		t.Fatal("new user must have onboarding_completed=false")
	}

	// GET preferences is empty before any save.
	rec := doJSON(t, h, http.MethodGet, "/api/v1/users/me/preferences", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET prefs: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	got := decodePrefs(t, rec)
	if len(got.Data.CategoryIDs) != 0 || got.Data.OnboardingCompleted {
		t.Fatalf("fresh user should have empty prefs and not completed: %+v", got.Data)
	}

	// Unauthenticated is 401.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/users/me/preferences", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET prefs: expected 401, got %d", rec.Code)
	}

	// Empty category list → 400 invalid_categories.
	rec = doJSON(t, h, http.MethodPut, "/api/v1/users/me/preferences", `{"category_ids":[]}`, token)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty prefs: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	// Unknown/inactive category → 400 invalid_categories.
	rec = doJSON(t, h, http.MethodPut, "/api/v1/users/me/preferences",
		`{"category_ids":["00000000-0000-0000-0000-000000000000"]}`, token)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown category: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	// A valid PUT replaces prefs and completes onboarding.
	rec = doJSON(t, h, http.MethodPut, "/api/v1/users/me/preferences",
		fmt.Sprintf(`{"category_ids":[%q,%q]}`, catID, catID), token)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT prefs: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	got = decodePrefs(t, rec)
	if len(got.Data.CategoryIDs) != 1 || got.Data.CategoryIDs[0] != catID {
		t.Fatalf("expected deduped single category, got %v", got.Data.CategoryIDs)
	}
	if !got.Data.OnboardingCompleted {
		t.Fatal("PUT prefs must complete onboarding")
	}
	if got.Data.UpdatedAt.IsZero() {
		t.Fatal("expected updated_at")
	}

	// /users/me now reports onboarding_completed.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/users/me", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("me: expected 200, got %d", rec.Code)
	}
	var me struct {
		Data struct {
			OnboardingCompleted bool `json:"onboarding_completed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if !me.Data.OnboardingCompleted {
		t.Fatal("me must report onboarding_completed=true after PUT prefs")
	}

	// Login after completion also surfaces the flag.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"email":%q,"password":"password123"}`, email), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login: expected 200, got %d", rec.Code)
	}
	login := decodeAuth(t, rec)
	if !login.Data.User.OnboardingCompleted {
		t.Fatal("login response must report onboarding_completed=true")
	}
}

func TestOnboardingSkipIdempotent(t *testing.T) {
	_, h := newTestApp(t)
	email := fmt.Sprintf("skips+%d@test.example", time.Now().UnixNano())
	reg := registerUser(t, h, email)
	token := reg.Data.AccessToken

	// Skip without touching preferences.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/users/me/onboarding/skip", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("skip: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodGet, "/api/v1/users/me", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("me: expected 200, got %d", rec.Code)
	}
	var me struct {
		Data struct {
			OnboardingCompleted bool `json:"onboarding_completed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if !me.Data.OnboardingCompleted {
		t.Fatal("skip must complete onboarding")
	}

	// Skipping again stays idempotent (200, still completed).
	rec = doJSON(t, h, http.MethodPost, "/api/v1/users/me/onboarding/skip", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("second skip: expected 200, got %d", rec.Code)
	}
}
