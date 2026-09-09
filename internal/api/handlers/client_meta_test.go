package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// clientMeta extracts the User-Agent and the client IP, honoring
// X-Forwarded-For when a reverse proxy sits in front. These tests lock the
// documented behavior: XFF first entry wins, RemoteAddr is the fallback.
func TestClientMetaNoForwarded(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.RemoteAddr = "203.0.113.9:44321"
	req.Header.Set("User-Agent", "curl/8.0")

	ua, ip := clientMeta(req)
	if ua != "curl/8.0" {
		t.Fatalf("user-agent = %q, want %q", ua, "curl/8.0")
	}
	if ip != "203.0.113.9:44321" {
		t.Fatalf("ip = %q, want RemoteAddr fallback", ip)
	}
}

func TestClientMetaForwardedSingle(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.RemoteAddr = "10.0.0.1:1000"
	req.Header.Set("X-Forwarded-For", "198.51.100.7")

	_, ip := clientMeta(req)
	if ip != "198.51.100.7" {
		t.Fatalf("ip = %q, want XFF value", ip)
	}
}

func TestClientMetaForwardedListTakesFirst(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.RemoteAddr = "10.0.0.1:1000"
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 203.0.113.9")

	_, ip := clientMeta(req)
	if ip != "198.51.100.7" {
		t.Fatalf("ip = %q, want first entry of comma list", ip)
	}
}
