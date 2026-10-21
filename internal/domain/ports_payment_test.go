package domain_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/daggix02-m/event_nu_backend/internal/domain"
)

// TestSignatureValid owns the webhook-seal contract (moved here with the port
// in hexagonal slice 3; the chapa adapter's own tests cover its HTTP mapping).
func TestSignatureValid(t *testing.T) {
	secret := "webhook-secret"
	body := []byte(`{"tx_ref":"abc","status":"success"}`)

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	good := hex.EncodeToString(mac.Sum(nil))

	if !domain.SignatureValid(secret, body, good) {
		t.Fatal("expected matching signature to validate")
	}
	if !domain.SignatureValid(secret, body, "junk", strings.ToUpper(good)) {
		t.Fatal("case-insensitive signature should validate next to an invalid one")
	}
	if domain.SignatureValid(secret, body, "junk") {
		t.Fatal("mismatched signature must fail")
	}
	if domain.SignatureValid(secret, []byte(`{"tx_ref":"other"}`), good) {
		t.Fatal("signature over a different body must fail")
	}
	if domain.SignatureValid("", body, good) {
		t.Fatal("empty secret must never validate")
	}
}
