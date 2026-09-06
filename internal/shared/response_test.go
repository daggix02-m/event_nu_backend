package shared

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSONEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusOK, map[string]string{"status": "ok"})

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}

	var body map[string]map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["data"]["status"] != "ok" {
		t.Fatalf("expected data.status=ok, got %v", body)
	}
}

func TestWriteErrorJSONShape(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteErrorJSON(rec, http.StatusForbidden, "forbidden", "no access")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error.Code != "forbidden" || body.Error.Message != "no access" {
		t.Fatalf("unexpected error body: %+v", body)
	}
}

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"known":"x","unknown":1}`))
	rec := httptest.NewRecorder()

	var dst struct {
		Known string `json:"known"`
	}
	err := DecodeJSON(rec, req, &dst)
	if err == nil {
		t.Fatal("expected unknown-field error")
	}
}

func TestDecodeJSONRejectsEmptyBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()

	var dst struct{}
	if err := DecodeJSON(rec, req, &dst); err == nil {
		t.Fatal("expected empty-body error")
	}
}

func TestDecodeJSONValid(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"ada"}`))
	rec := httptest.NewRecorder()

	var dst struct {
		Name string `json:"name"`
	}
	if err := DecodeJSON(rec, req, &dst); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dst.Name != "ada" {
		t.Fatalf("expected name=ada, got %q", dst.Name)
	}
}
