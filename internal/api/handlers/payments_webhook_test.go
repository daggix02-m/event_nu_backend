package handlers_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/api"
	"github.com/daggix02-m/event_nu/backend/internal/api/routes"
	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/payments"
	"github.com/daggix02-m/event_nu/backend/internal/repository"
	"github.com/daggix02-m/event_nu/backend/internal/service"
)

// ---- fake provider -------------------------------------------------------------

// fakeProvider implements payments.Provider with scripted Initialize/Verify so
// tests exercise the full order→checkout→webhook pipeline without a network.
type fakeProvider struct {
	mu      sync.Mutex
	name    string
	initErr error
	verify  map[string]payments.VerifyResult
	init    int
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Initialize(ctx context.Context, req payments.InitializeRequest) (payments.InitializeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.init++
	if f.initErr != nil {
		return payments.InitializeResult{}, f.initErr
	}
	return payments.InitializeResult{
		ProviderRef: req.TxRef,
		RedirectURL: "https://checkout.example/pay/" + req.TxRef,
	}, nil
}

func (f *fakeProvider) Verify(ctx context.Context, txRef string) (payments.VerifyResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.verify[txRef]
	if !ok {
		return payments.VerifyResult{}, fmt.Errorf("no verification for %s", txRef)
	}
	return v, nil
}

const testWebhookSecret = "phase-15-test-webhook-secret"

func signWebhook(body string) string {
	mac := hmac.New(sha256.New, []byte(testWebhookSecret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

func doSignedWebhook(t *testing.T, h http.Handler, body, signature string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/payments/chapa", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if signature != "" {
		req.Header.Set("Chapa-Signature", signature)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// fakePaymentApp builds the app with PAYMENT_PROVIDER=chapa, then swaps the
// real provider for the fake via a rebuilt PaymentService and route table.
func fakePaymentApp(t *testing.T, fake *fakeProvider) (*api.Application, http.Handler) {
	t.Helper()
	loadEnvForTest(t)
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping DB-backed payment integration tests")
	}
	pool, err := database.NewPool(context.Background(), dsn)
	if err != nil {
		t.Skipf("database unreachable (%v) — skipping DB-backed payment integration tests", err)
	}
	t.Cleanup(pool.Close)

	t.Setenv("PAYMENT_PROVIDER", "chapa")
	t.Setenv("CHAPA_SECRET_KEY", "test-secret-key")
	t.Setenv("CHAPA_WEBHOOK_SECRET", testWebhookSecret)
	t.Setenv("CHAPA_API_BASE", "https://fake-api.example/v1")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	app := api.NewApp(testLogger(), cfg, pool)
	t.Cleanup(app.RateLimiter.Stop)

	app.Payment = service.NewPaymentService(
		repository.NewOrderRepository(pool),
		repository.NewPaymentRepository(pool),
		repository.NewUserRepository(pool),
		fake,
		app.Notify,
		cfg,
	)
	return app, routes.New(app)
}

// servicePaymentRepo opens the ledger with a service-role pool so tests can
// assert the payments table the privileged webhook writes.
func servicePaymentRepo(t *testing.T) *repository.PaymentRepository {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set")
	}
	sp, err := database.NewPoolWithRole(context.Background(), os.Getenv("DATABASE_URL"), "service")
	if err != nil {
		t.Fatalf("service pool: %v", err)
	}
	t.Cleanup(sp.Close)
	return repository.NewPaymentRepository(sp)
}

// ---- wire shapes ---------------------------------------------------------------

type paymentInitWire struct {
	Provider    string `json:"provider"`
	ProviderRef string `json:"provider_ref"`
	RedirectURL string `json:"redirect_url"`
}

type orderPaymentWire struct {
	ID              string           `json:"id"`
	Status          string           `json:"status"`
	PaymentProvider *string          `json:"payment_provider,omitempty"`
	ProviderRef     *string          `json:"provider_ref,omitempty"`
	Payment         *paymentInitWire `json:"payment,omitempty"`
}

func decodeOrderWithPayment(t *testing.T, rec *httptest.ResponseRecorder) orderPaymentWire {
	t.Helper()
	var env struct {
		Data orderPaymentWire `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode order: %v; body=%s", err, rec.Body.String())
	}
	return env.Data
}

func countNotifications(t *testing.T, h http.Handler, tok, ntype string) int {
	t.Helper()
	rec := doJSON(t, h, http.MethodGet, "/api/v1/notifications?limit=100", "", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("notifications: expected 200, got %d", rec.Code)
	}
	var env struct {
		Data []struct {
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode notifications: %v", err)
	}
	n := 0
	for _, it := range env.Data {
		if it.Type == ntype {
			n++
		}
	}
	return n
}

// ---- tests --------------------------------------------------------------------

func TestPaymentInitAndWebhookSuccess(t *testing.T) {
	fake := &fakeProvider{name: "chapa", verify: map[string]payments.VerifyResult{}}
	_, h := fakePaymentApp(t, fake)

	orgTok, _ := promoteOrganizer(t, h, "payo", fmt.Sprintf("Pay Org %d", time.Now().UnixNano()))
	start := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	eventID := createPublishedEvent(t, h, orgTok, start)
	ga := createTier(t, h, orgTok, eventID,
		`{"name":"GA","price_minor":1000,"currency":"ETB","quantity_total":5}`)

	buyTok := registerUser(t, h, fmt.Sprintf("paybuyer+%d@test.example", time.Now().UnixNano())).Data.AccessToken

	// Card-init returns the checkout the buyer must be sent to.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/orders", orderBody(ga, 1), buyTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create order: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	order := decodeOrderWithPayment(t, rec)
	if order.Status != "pending" {
		t.Fatalf("expected pending order, got %q", order.Status)
	}
	if order.Payment == nil || order.Payment.Provider != "chapa" || !strings.HasPrefix(order.Payment.RedirectURL, "https://checkout.example/pay/") {
		t.Fatalf("expected payment block on order create, got %+v", order.Payment)
	}
	if order.Payment.ProviderRef != order.ID {
		t.Fatalf("expected provider_ref = order id, got %q", order.Payment.ProviderRef)
	}

	// Not finalized yet: no tickets, no paid/failed ledger state, and the
	// orders provider columns are still empty (the card-init ledger row IS
	// present, pending).
	if rec = doJSON(t, h, http.MethodGet, "/api/v1/orders/"+order.ID, "", buyTok); rec.Code != http.StatusOK {
		t.Fatalf("get order: expected 200, got %d", rec.Code)
	}
	before := decodeOrderWithPayment(t, rec)
	if before.PaymentProvider != nil || before.ProviderRef != nil {
		t.Fatalf("expected empty provider columns pre-webhook, got %+v", before)
	}
	pendingLedger, err := servicePaymentRepo(t).GetByOrder(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("expected pending ledger row post card-init: %v", err)
	}
	if pendingLedger.Status != domain.PaymentStatusPending || pendingLedger.AmountMinor != 1000 || pendingLedger.Currency != "ETB" {
		t.Fatalf("unexpected pending ledger: %+v", pendingLedger)
	}
	if rec = doJSON(t, h, http.MethodGet, "/api/v1/me/tickets", "", buyTok); len(decodeTickets(t, rec)) != 0 {
		t.Fatalf("expected empty wallet pre-payment")
	}

	// Signed success webhook: plays out from the provider's verify response.
	body := fmt.Sprintf(`{"tx_ref":%q,"status":"successful"}`, order.ID)
	fake.verify[order.ID] = payments.VerifyResult{
		Status: payments.StatusSuccess, AmountMinor: 1000, Currency: "ETB",
	}
	if rec = doSignedWebhook(t, h, body, signWebhook(body)); rec.Code != http.StatusOK {
		t.Fatalf("webhook: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Order paid, tickets issued, ledger row written with the money facts.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/orders/"+order.ID, "", buyTok)
	paid := decodeOrderWithPayment(t, rec)
	if paid.Status != "paid" || paid.PaymentProvider == nil || *paid.PaymentProvider != "chapa" ||
		paid.ProviderRef == nil || *paid.ProviderRef != order.ID {
		t.Fatalf("unexpected paid order: %+v", paid)
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/tickets", "", buyTok)
	if len(decodeTickets(t, rec)) != 1 {
		t.Fatalf("expected one issued ticket, got %s", rec.Body.String())
	}
	ledger, err := servicePaymentRepo(t).GetByOrder(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("ledger row: %v", err)
	}
	if ledger.Status != domain.PaymentStatusPaid || ledger.AmountMinor != 1000 || ledger.Currency != "ETB" {
		t.Fatalf("unexpected ledger: %+v", ledger)
	}

	// Buyer got the confirmation notification.
	if countNotifications(t, h, buyTok, "ticket") != 1 {
		t.Fatal("expected one ticket confirmation notification")
	}

	// The provider retrying the same event is an idempotent no-op: still one
	// ticket, still one notification, ledger still paid.
	if rec = doSignedWebhook(t, h, body, signWebhook(body)); rec.Code != http.StatusOK {
		t.Fatalf("duplicate webhook: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/tickets", "", buyTok)
	if len(decodeTickets(t, rec)) != 1 {
		t.Fatalf("duplicate webhook re-issued tickets: %s", rec.Body.String())
	}
	if countNotifications(t, h, buyTok, "ticket") != 1 {
		t.Fatal("duplicate webhook duplicated the notification")
	}
}

func TestPaymentWebhookRejectsTamperedAndUnknown(t *testing.T) {
	fake := &fakeProvider{name: "chapa", verify: map[string]payments.VerifyResult{}}
	_, h := fakePaymentApp(t, fake)

	body := `{"tx_ref":"00000000-0000-0000-0000-000000000001","status":"successful"}`

	// Tampered body: signature checks out against a different payload.
	other := `{"tx_ref":"00000000-0000-0000-0000-000000000099","status":"successful"}`
	if rec := doSignedWebhook(t, h, body, signWebhook(other)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered signature: expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
	// Missing signature header.
	if rec := doSignedWebhook(t, h, body, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing signature: expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
	// Signature over a body that is not JSON.
	notJSON := `this is not json`
	if rec := doSignedWebhook(t, h, notJSON, signWebhook(notJSON)); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed payload: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPaymentWebhookUnknownOrderIsNotFound(t *testing.T) {
	fake := &fakeProvider{name: "chapa", verify: map[string]payments.VerifyResult{}}
	_, h := fakePaymentApp(t, fake)

	unknown := "00000000-0000-0000-0000-00000000dead"
	body := fmt.Sprintf(`{"tx_ref":%q,"status":"successful"}`, unknown)
	fake.verify[unknown] = payments.VerifyResult{Status: payments.StatusSuccess, AmountMinor: 100, Currency: "ETB"}

	if rec := doSignedWebhook(t, h, body, signWebhook(body)); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown order: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPaymentFailureReleasesInventoryAndLedgers(t *testing.T) {
	fake := &fakeProvider{name: "chapa", verify: map[string]payments.VerifyResult{}}
	_, h := fakePaymentApp(t, fake)

	orgTok, _ := promoteOrganizer(t, h, "payf", fmt.Sprintf("PayF Org %d", time.Now().UnixNano()))
	start := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	eventID := createPublishedEvent(t, h, orgTok, start)
	ga := createTier(t, h, orgTok, eventID,
		`{"name":"GA","price_minor":1000,"currency":"ETB","quantity_total":1}`)

	buyTok := registerUser(t, h, fmt.Sprintf("payfail+%d@test.example", time.Now().UnixNano())).Data.AccessToken

	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/orders", orderBody(ga, 1), buyTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create order: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	order := decodeOrderWithPayment(t, rec)

	// Failed webhook (authoritative from verify) flips the order and releases
	// the held inventory unit.
	body := fmt.Sprintf(`{"tx_ref":%q,"status":"failed"}`, order.ID)
	fake.verify[order.ID] = payments.VerifyResult{
		Status: payments.StatusFailed, AmountMinor: 1000, Currency: "ETB",
	}
	if rec = doSignedWebhook(t, h, body, signWebhook(body)); rec.Code != http.StatusOK {
		t.Fatalf("failed webhook: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodGet, "/api/v1/orders/"+order.ID, "", buyTok)
	failed := decodeOrderWithPayment(t, rec)
	if failed.Status != "failed" {
		t.Fatalf("expected failed order, got %q", failed.Status)
	}
	ledger, err := servicePaymentRepo(t).GetByOrder(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("ledger row: %v", err)
	}
	if ledger.Status != domain.PaymentStatusFailed || ledger.AmountMinor != 1000 || ledger.Currency != "ETB" {
		t.Fatalf("unexpected failed ledger: %+v", ledger)
	}

	// The released unit is buyable: a fresh order (post-failure) succeeds.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/orders", orderBody(ga, 1), buyTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("reorder after failure: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPaymentInitFailureRollsBackOrderAndReservation(t *testing.T) {
	fake := &fakeProvider{name: "chapa", verify: map[string]payments.VerifyResult{}, initErr: fmt.Errorf("provider down")}
	_, h := fakePaymentApp(t, fake)

	orgTok, _ := promoteOrganizer(t, h, "payx", fmt.Sprintf("PayX Org %d", time.Now().UnixNano()))
	start := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	eventID := createPublishedEvent(t, h, orgTok, start)
	ga := createTier(t, h, orgTok, eventID,
		`{"name":"GA","price_minor":1000,"currency":"ETB","quantity_total":1}`)

	buyTok := registerUser(t, h, fmt.Sprintf("payinit+%d@test.example", time.Now().UnixNano())).Data.AccessToken

	// Provider is down: the 502 rolls back the whole request, so the order and
	// its inventory reservation both vanish.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/orders", orderBody(ga, 1), buyTok)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("init failure: expected 502, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/orders", "", buyTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("my orders: expected 200, got %d", rec.Code)
	}
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode my orders: %v", err)
	}
	if len(env.Data) != 0 {
		t.Fatalf("expected the failed-init order to be rolled back, found %d", len(env.Data))
	}

	// Inventory was not leaked: a fresh order fits.
	fake.initErr = nil
	rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/orders", orderBody(ga, 1), buyTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("reorder after init failure: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}
