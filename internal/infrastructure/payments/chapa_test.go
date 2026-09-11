package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSignatureValid(t *testing.T) {
	secret := "webhook-secret"
	body := []byte(`{"tx_ref":"abc","status":"success"}`)

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	good := hex.EncodeToString(mac.Sum(nil))

	if !SignatureValid(secret, body, good) {
		t.Fatal("expected matching signature to validate")
	}
	if !SignatureValid(secret, body, "junk", strings.ToUpper(good)) {
		t.Fatal("case-insensitive signature should validate next to an invalid one")
	}
	if SignatureValid(secret, body, "junk") {
		t.Fatal("mismatched signature must fail")
	}
	if SignatureValid(secret, []byte(`{"tx_ref":"other"}`), good) {
		t.Fatal("signature over a different body must fail")
	}
	if SignatureValid("", body, good) {
		t.Fatal("empty secret must never validate")
	}
}

func TestFormatMajor(t *testing.T) {
	cases := []struct {
		minor int64
		want  string
	}{
		{0, "0"},
		{1200, "12"},
		{1250, "12.5"},
		{1299, "12.99"},
		{5, "0.05"},
		{50, "0.5"},
	}
	for _, c := range cases {
		if got := formatMajor(c.minor); got != c.want {
			t.Errorf("formatMajor(%d) = %q, want %q", c.minor, got, c.want)
		}
	}
}

func TestParseMajor(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"12", 1200},
		{"12.5", 1250},
		{"12.99", 1299},
		{"0.05", 5},
		{"", 0},
		{"garbage", 0},
	}
	for _, c := range cases {
		if got := parseMajor(c.in); got != c.want {
			t.Errorf("parseMajor(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestChapaInitializeStartsCheckout(t *testing.T) {
	var gotBody map[string]any
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"Hosted Payment","status":"success","data":{"checkout_url":"https://checkout.chapa.co/checkout/payment/abc"}}`))
	}))
	defer srv.Close()

	p := NewChapa(srv.URL, "CHASECK_TEST-xyz")
	res, err := p.Initialize(context.Background(), InitializeRequest{
		TxRef:       "order-uuid",
		AmountMinor: 1250,
		Currency:    "ETB",
		CallbackURL: "https://api.example/webhooks/payments/chapa",
		ReturnURL:   "https://example.com/orders/order-uuid",
	})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if gotAuth != "Bearer CHASECK_TEST-xyz" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if gotBody["tx_ref"] != "order-uuid" {
		t.Errorf("tx_ref = %v", gotBody["tx_ref"])
	}
	if gotBody["amount"] != "12.5" {
		t.Errorf("amount = %v, want 12.5", gotBody["amount"])
	}
	if gotBody["currency"] != "ETB" {
		t.Errorf("currency = %v", gotBody["currency"])
	}
	custom, ok := gotBody["customization"].(map[string]any)
	if !ok || custom["title"] != "Event Nu order" {
		t.Errorf("customization = %v", gotBody["customization"])
	}
	if res.ProviderRef != "order-uuid" || res.RedirectURL != "https://checkout.chapa.co/checkout/payment/abc" {
		t.Errorf("result = %+v", res)
	}
}

func TestChapaInitializeRejectsFailure(t *testing.T) {
	t.Run("http error status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"bad","status":"failed"}`))
		}))
		defer srv.Close()

		p := NewChapa(srv.URL, "k")
		if _, err := p.Initialize(context.Background(), InitializeRequest{TxRef: "x", AmountMinor: 100, Currency: "ETB"}); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("missing checkout url", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"message":"ok","status":"success","data":{}}`))
		}))
		defer srv.Close()

		p := NewChapa(srv.URL, "k")
		if _, err := p.Initialize(context.Background(), InitializeRequest{TxRef: "x", AmountMinor: 100, Currency: "ETB"}); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestChapaVerify(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.Contains(r.URL.Path, "/transaction/verify/tx-1") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"Transaction fetched successfully","status":"success","data":{"tx_ref":"tx-1","status":"success","amount":"12.5","currency":"ETB"}}`))
	}))
	defer srv.Close()

	p := NewChapa(srv.URL, "k")
	res, err := p.Verify(context.Background(), "tx-1")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Status != StatusSuccess || res.AmountMinor != 1250 || res.Currency != "ETB" {
		t.Errorf("result = %+v", res)
	}
}

func TestNormalizeStatus(t *testing.T) {
	cases := map[string]string{
		"success":           StatusSuccess,
		"failed":            StatusFailed,
		"cancelled_by_user": StatusFailed,
		"pending":           StatusPending,
		"authorized":        StatusPending,
		"something_unknown": StatusPending,
		"":                  StatusPending,
	}
	for in, want := range cases {
		if got := normalizeStatus(in); got != want {
			t.Errorf("normalizeStatus(%q) = %q, want %q", in, got, want)
		}
	}
}
