package payments

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	chapaName        = "chapa"
	chapaDefaultBase = "https://api.chapa.co/v1"
	chapaHTTPTimeout = 10 * time.Second
	// Chapa moves money in the currency's major unit (Birr), while the app
	// stores minor units (cents).
	chapaDecimals = 2
)

// ChapaProvider talks to the Chapa payment gateway (https://developer.chapa.co).
// Initialize POSTs to /transaction/initialize; Verify GETs
// /transaction/verify/{tx_ref}; webhook authenticity is checked separately with
// the webhook secret (see SignatureValid).
type ChapaProvider struct {
	baseURL   string
	secretKey string
	client    *http.Client
}

func NewChapa(baseURL, secretKey string) *ChapaProvider {
	if baseURL == "" {
		baseURL = chapaDefaultBase
	}
	return &ChapaProvider{
		baseURL:   strings.TrimSuffix(baseURL, "/"),
		secretKey: secretKey,
		client:    &http.Client{Timeout: chapaHTTPTimeout},
	}
}

func (p *ChapaProvider) Name() string { return chapaName }

type chapaInitRequest struct {
	Amount        string          `json:"amount"`
	Currency      string          `json:"currency"`
	Email         string          `json:"email,omitempty"`
	FirstName     string          `json:"first_name,omitempty"`
	LastName      string          `json:"last_name,omitempty"`
	TxRef         string          `json:"tx_ref"`
	CallbackURL   string          `json:"callback_url"`
	ReturnURL     string          `json:"return_url"`
	Customization chapaCustomiztn `json:"customization"`
}

type chapaCustomiztn struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type chapaInitResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    struct {
		CheckoutURL string `json:"checkout_url"`
	} `json:"data"`
}

func (p *ChapaProvider) Initialize(ctx context.Context, req InitializeRequest) (InitializeResult, error) {
	body, err := json.Marshal(chapaInitRequest{
		Amount:      formatMajor(req.AmountMinor),
		Currency:    req.Currency,
		Email:       req.Email,
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		TxRef:       req.TxRef,
		CallbackURL: req.CallbackURL,
		ReturnURL:   req.ReturnURL,
		Customization: chapaCustomiztn{
			Title:       "Event Nu order",
			Description: req.Description,
		},
	})
	if err != nil {
		return InitializeResult{}, fmt.Errorf("chapa init: encode body: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/transaction/initialize", bytes.NewReader(body))
	if err != nil {
		return InitializeResult{}, fmt.Errorf("chapa init: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.secretKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return InitializeResult{}, fmt.Errorf("chapa init: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return InitializeResult{}, fmt.Errorf("chapa init: read response: %w", err)
	}

	var parsed chapaInitResponse
	if resp.StatusCode >= 400 {
		return InitializeResult{}, fmt.Errorf("chapa init: status %d: %s", resp.StatusCode, truncate(raw))
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return InitializeResult{}, fmt.Errorf("chapa init: decode response: %w", err)
	}
	if parsed.Status != "success" || parsed.Data.CheckoutURL == "" {
		return InitializeResult{}, fmt.Errorf("chapa init: provider rejected checkout (%s)", parsed.Message)
	}

	return InitializeResult{
		ProviderRef: req.TxRef,
		RedirectURL: parsed.Data.CheckoutURL,
	}, nil
}

type chapaVerifyResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    struct {
		Status   string `json:"status"`
		TxRef    string `json:"tx_ref"`
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	} `json:"data"`
}

func (p *ChapaProvider) Verify(ctx context.Context, providerRef string) (VerifyResult, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		p.baseURL+"/transaction/verify/"+providerRef, nil)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("chapa verify: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.secretKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("chapa verify: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return VerifyResult{}, fmt.Errorf("chapa verify: read response: %w", err)
	}

	var parsed chapaVerifyResponse
	if resp.StatusCode >= 400 {
		return VerifyResult{}, fmt.Errorf("chapa verify: status %d: %s", resp.StatusCode, truncate(raw))
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return VerifyResult{}, fmt.Errorf("chapa verify: decode response: %w", err)
	}

	return VerifyResult{
		Status:      normalizeStatus(parsed.Data.Status),
		AmountMinor: parseMajor(parsed.Data.Amount),
		Currency:    strings.ToUpper(parsed.Data.Currency),
	}, nil
}

// formatMajor renders a minor-unit amount as the provider's major-unit string
// with up to chapaDecimals digits (1250 cents -> "12.5", 1200 -> "12").
func formatMajor(amountMinor int64) string {
	scale := int64(1)
	for i := 0; i < chapaDecimals; i++ {
		scale *= 10
	}
	whole := amountMinor / scale
	frac := amountMinor % scale
	if frac == 0 {
		return strconv.FormatInt(whole, 10)
	}
	return strconv.FormatInt(whole, 10) + "." + padFraction(frac, chapaDecimals)
}

func padFraction(frac int64, width int) string {
	s := strconv.FormatInt(frac, 10)
	for len(s) < width {
		s = "0" + s
	}
	return strings.TrimRight(s, "0")
}

// parseMajor parses a major-unit string ("12.5", "12") into minor units.
func parseMajor(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	parts := strings.SplitN(s, ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0
	}
	frac := int64(0)
	if len(parts) == 2 {
		f := parts[1]
		if len(f) > chapaDecimals {
			f = f[:chapaDecimals]
		}
		for _, c := range f {
			if c < '0' || c > '9' {
				frac = 0
				f = ""
				break
			}
			frac = frac*10 + int64(c-'0')
		}
		for i := len(f); i < chapaDecimals; i++ {
			frac *= 10
		}
	}
	v := whole*int64Pow10(chapaDecimals) + frac
	if neg {
		v = -v
	}
	return v
}

func int64Pow10(n int) int64 {
	v := int64(1)
	for i := 0; i < n; i++ {
		v *= 10
	}
	return v
}

func normalizeStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "success", "completed", "paid":
		return StatusSuccess
	case "failed", "cancelled", "cancelled_by_user", "abandoned", "expired":
		return StatusFailed
	default:
		return StatusPending
	}
}

func truncate(b []byte) string {
	const limit = 512
	if len(b) > limit {
		return string(b[:limit]) + "..."
	}
	return string(b)
}

// SignatureValid verifies a Chapa webhook signature: HMAC-SHA256 of the raw
// request body using the webhook secret, hex-encoded. Chapa sends the header
// either as "Chapa-Signature" or "x-chapa-signature"; any single matching
// value suffices. Comparison is constant-time.
func SignatureValid(secret string, body []byte, provided ...string) bool {
	if secret == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)
	expectedHex := make([]byte, hex.EncodedLen(len(expected)))
	hex.Encode(expectedHex, expected)

	for _, p := range provided {
		if p == "" {
			continue
		}
		if hmac.Equal([]byte(strings.TrimSpace(strings.ToLower(p))), expectedHex) {
			return true
		}
	}
	return false
}

var _ Provider = (*ChapaProvider)(nil)
