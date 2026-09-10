package handlers_test

import (
	"context"
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
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu/backend/internal/repository"
	"github.com/daggix02-m/event_nu/backend/internal/service"
)

// ---- wire shapes --------------------------------------------------------------

type ticketTypeWire struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Currency     string `json:"currency"`
	Quantity     *int   `json:"quantity_total"`
	SoldOut      bool   `json:"sold_out"`
	SalesOpen    bool   `json:"sales_open"`
	IsActive     bool   `json:"is_active"`
	QuantitySold int    `json:"quantity_sold"`
}

type orderWire struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Currency string `json:"currency"`
	Subtotal int64  `json:"subtotal_minor"`
	Total    int64  `json:"total_minor"`
	Items    []struct {
		TicketTypeID string `json:"ticket_type_id"`
		TicketType   string `json:"ticket_type"`
		Quantity     int    `json:"quantity"`
		UnitPrice    int64  `json:"unit_price"`
		Subtotal     int64  `json:"subtotal"`
	} `json:"items"`
}

type ticketWire struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	QR     struct {
		TicketID string `json:"ticket_id"`
		EventID  string `json:"event_id"`
		IssuedAt string `json:"issued_at"`
		HMAC     string `json:"hmac"`
	} `json:"qr"`
}

func decodeOrder(t *testing.T, rec *httptest.ResponseRecorder) orderWire {
	t.Helper()
	var env struct {
		Data orderWire `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode order: %v; body=%s", err, rec.Body.String())
	}
	return env.Data
}

func decodeTickets(t *testing.T, rec *httptest.ResponseRecorder) []ticketWire {
	t.Helper()
	var env struct {
		Data []ticketWire `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode tickets: %v; body=%s", err, rec.Body.String())
	}
	return env.Data
}

func decodeTicketTypes(t *testing.T, rec *httptest.ResponseRecorder) []ticketTypeWire {
	t.Helper()
	var env struct {
		Data []ticketTypeWire `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode ticket types: %v; body=%s", err, rec.Body.String())
	}
	return env.Data
}

// serviceOrderService returns an OrderService bound to a service-role pool so
// tests can drive the privileged ConfirmPaid path (the Phase 15 webhook's job).
func serviceOrderService(t *testing.T, app *api.Application) *service.OrderService {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set")
	}
	sp, err := database.NewPoolWithRole(context.Background(), os.Getenv("DATABASE_URL"), "service")
	if err != nil {
		t.Fatalf("service pool: %v", err)
	}
	t.Cleanup(sp.Close)
	return service.NewOrderService(
		repository.NewOrderRepository(sp),
		repository.NewTicketTypeRepository(sp),
		repository.NewEventRepository(sp),
		repository.NewOrganizerRepository(sp),
		app.Config.TicketQRSecret,
	)
}

// createTier posts a ticket tier and returns its id (or fails).
func createTier(t *testing.T, h http.Handler, orgTok, eventID, body string) string {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/ticket-types", body, orgTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create ticket type: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	return idFromCreate(t, rec, "id")
}

func orderBody(ticketTypeID string, qty int) string {
	return fmt.Sprintf(`{"items":[{"ticket_type_id":%q,"quantity":%d}]}`, ticketTypeID, qty)
}

// ---- ticket types -------------------------------------------------------------

func TestTicketTypesCRUDAndVisibility(t *testing.T) {
	_, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "ttz", fmt.Sprintf("TT Org %d", time.Now().UnixNano()))
	start := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	eventID := createPublishedEvent(t, h, orgTok, start)

	// Organizer of another event is not a creator here.
	otherTok, _ := promoteOrganizer(t, h, "otz", fmt.Sprintf("TT Other %d", time.Now().UnixNano()))
	if rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/ticket-types",
		`{"name":"Nope","price_minor":0,"currency":"ETB"}`, otherTok); rec.Code != http.StatusForbidden {
		t.Fatalf("foreign organizer create: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	qty5 := 5
	ga := createTier(t, h, orgTok, eventID, fmt.Sprintf(
		`{"name":"General","description":"GA","price_minor":1000,"currency":"etb","quantity_total":%d}`, qty5))
	vipID := createTier(t, h, orgTok, eventID,
		`{"name":"VIP","description":"VIP","price_minor":5000,"currency":"ETB"}`)

	// Invalid tier: negative price and bad currency rejected with 422.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/ticket-types",
		`{"name":"Bad","price_minor":-1,"currency":"E","quantity_total":0}`, orgTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid tier: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// Public listing (no auth) shows both tiers, uppercased currency, selling.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/events/"+eventID+"/ticket-types", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("public tiers: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	publics := decodeTicketTypes(t, rec)
	if len(publics) != 2 {
		t.Fatalf("expected 2 public tiers, got %d", len(publics))
	}
	for _, tt := range publics {
		if tt.Currency != "ETB" {
			t.Fatalf("expected ETB currency, got %q", tt.Currency)
		}
		if tt.SoldOut || !tt.SalesOpen {
			t.Fatalf("expected selling and not sold out, got sold_out=%v sales_open=%v", tt.SoldOut, tt.SalesOpen)
		}
	}

	// Deactivate the VIP tier: it disappears from the public list but remains
	// visible to the organizer's manage list.
	patchGA := fmt.Sprintf(`{"name":"General","price_minor":1000,"quantity_total":%d,"is_active":true}`, qty5)
	if rec = doJSON(t, h, http.MethodPatch, "/api/v1/events/"+eventID+"/ticket-types/"+ga, patchGA, orgTok); rec.Code != http.StatusOK {
		t.Fatalf("patch tier: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	vipPatch := fmt.Sprintf(`{"name":"VIP","price_minor":5000,"quantity_total":%d,"is_active":false}`, qty5)
	if rec = doJSON(t, h, http.MethodPatch, "/api/v1/events/"+eventID+"/ticket-types/"+vipID, vipPatch, orgTok); rec.Code != http.StatusOK {
		t.Fatalf("deactivate tier: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodGet, "/api/v1/events/"+eventID+"/ticket-types", "", "")
	publicsAfter := decodeTicketTypes(t, rec)
	if len(publicsAfter) != 1 || publicsAfter[0].Name != "General" {
		t.Fatalf("expected only the active tier, got %+v", publicsAfter)
	}

	rec = doJSON(t, h, http.MethodGet, "/api/v1/events/"+eventID+"/ticket-types/manage", "", orgTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("manage tiers: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	manage := decodeTicketTypes(t, rec)
	if len(manage) != 2 {
		t.Fatalf("expected 2 managed tiers, got %d", len(manage))
	}

	// Public readers cannot see catalog or manage.
	if rec = doJSON(t, h, http.MethodGet, "/api/v1/events/"+eventID+"/ticket-types/manage", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth manage tiers: expected 401, got %d", rec.Code)
	}
}

// ---- order lifecycle + check-in -----------------------------------------------

func TestOrderLifecycleCheckIn(t *testing.T) {
	app, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "clz", fmt.Sprintf("CL Org %d", time.Now().UnixNano()))
	start := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	eventID := createPublishedEvent(t, h, orgTok, start)

	price := int64(100)
	ga := createTier(t, h, orgTok, eventID, fmt.Sprintf(
		`{"name":"GA","price_minor":%d,"currency":"ETB","quantity_total":1}`, price))

	email := fmt.Sprintf("buyer+%d@test.example", time.Now().UnixNano())
	reg := registerUser(t, h, email)
	buyTok := reg.Data.AccessToken

	// Order before payment: pending, priced in ETB, correct line math.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/orders", orderBody(ga, 1), buyTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create order: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	order := decodeOrder(t, rec)
	if order.Status != "pending" || order.Currency != "ETB" || order.Subtotal != price || order.Total != price {
		t.Fatalf("unexpected order: %+v", order)
	}
	if len(order.Items) != 1 || order.Items[0].Subtotal != price || order.Items[0].TicketType != "GA" {
		t.Fatalf("unexpected order items: %+v", order.Items)
	}

	// No tickets until payment confirms.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/tickets", "", buyTok)
	if rec.Code != http.StatusOK || len(decodeTickets(t, rec)) != 0 {
		t.Fatalf("expected empty wallet pre-payment, got %d: %s", rec.Code, rec.Body.String())
	}

	// The organizer can view the buyer's order; a stranger cannot.
	if rec = doJSON(t, h, http.MethodGet, "/api/v1/orders/"+order.ID, "", orgTok); rec.Code != http.StatusOK {
		t.Fatalf("organizer view order: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Payment confirmation (Phase 15 webhook path) issues the ticket.
	paid, tickets, err := serviceOrderService(t, app).ConfirmPaid(context.Background(), order.ID, "chapa", "chp-test-1")
	if err != nil {
		t.Fatalf("confirm paid: %v", err)
	}
	if paid.Status != "paid" || len(tickets) != 1 {
		t.Fatalf("unexpected paid state: status=%s tickets=%d", paid.Status, len(tickets))
	}

	// Wallet now exposes the signed QR payload.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/me/tickets", "", buyTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("my tickets: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	wallet := decodeTickets(t, rec)
	if len(wallet) != 1 || wallet[0].Status != "issued" {
		t.Fatalf("expected one issued ticket, got %+v", wallet)
	}
	qr := wallet[0].QR
	issuedAt, err := time.Parse(time.RFC3339, qr.IssuedAt)
	if err != nil {
		t.Fatalf("issued_at not RFC3339: %q", qr.IssuedAt)
	}
	expectHMAC := domain.SignTicketPayload(qr.TicketID, qr.EventID, issuedAt, app.Config.TicketQRSecret)
	if qr.HMAC != expectHMAC {
		t.Fatalf("qr hmac mismatch: got %q want %q", qr.HMAC, expectHMAC)
	}

	checkInBody := fmt.Sprintf(`{"ticket_id":%q,"event_id":%q,"issued_at":%q,"hmac":%q}`,
		qr.TicketID, qr.EventID, qr.IssuedAt, qr.HMAC)

	// Happy-path check-in.
	if rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/check-in", checkInBody, orgTok); rec.Code != http.StatusOK {
		t.Fatalf("check-in: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var checkedEnv struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &checkedEnv); err != nil || checkedEnv.Data.Status != "ok" {
		t.Fatalf("check-in body: %s", rec.Body.String())
	}

	// Re-scan is rejected as already checked in.
	if rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/check-in", checkInBody, orgTok); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate check-in: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// Tampered payloads fail as invalid codes, not 5xx.
	tampered := fmt.Sprintf(`{"ticket_id":%q,"event_id":%q,"issued_at":%q,"hmac":"deadbeef"}`,
		qr.TicketID, qr.EventID, qr.IssuedAt)
	if rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/check-in", tampered, orgTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad hmac: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
	badDate := fmt.Sprintf(`{"ticket_id":%q,"event_id":%q,"issued_at":"01-02-2020","hmac":%q}`,
		qr.TicketID, qr.EventID, qr.HMAC)
	if rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/check-in", badDate, orgTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad date: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
	// A validly-signed ticket for a different event must not admit here.
	otherQR := fmt.Sprintf(`{"ticket_id":%q,"event_id":%q,"issued_at":%q,"hmac":%q}`,
		qr.TicketID, "00000000-0000-0000-0000-000000000000", qr.IssuedAt,
		domain.SignTicketPayload(qr.TicketID, "00000000-0000-0000-0000-000000000000", issuedAt, app.Config.TicketQRSecret))
	if rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/check-in", otherQR, orgTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cross-event ticket: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// Non-organizers cannot check in.
	if rec = doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/check-in", checkInBody, buyTok); rec.Code != http.StatusForbidden {
		t.Fatalf("buyer check-in: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---- inventory atomicity + cancellation ---------------------------------------

func TestOrderInventoryAtomicityAndCancel(t *testing.T) {
	app, h := newTestApp(t)
	orgTok, _ := promoteOrganizer(t, h, "avz", fmt.Sprintf("AV Org %d", time.Now().UnixNano()))
	start := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	eventID := createPublishedEvent(t, h, orgTok, start)

	ga := createTier(t, h, orgTok, eventID,
		`{"name":"GA","price_minor":0,"currency":"ETB","quantity_total":5}`)

	// Two buyers race for 3+3 against a capacity of 5; exactly one wins.
	buyers := []string{
		registerUser(t, h, fmt.Sprintf("racer+%d@test.example", time.Now().UnixNano())).Data.AccessToken,
		registerUser(t, h, fmt.Sprintf("racer2+%d@test.example", time.Now().UnixNano())).Data.AccessToken,
	}
	type result struct {
		code int
		body string
		tok  string
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, tok := range buyers {
		wg.Add(1)
		go func(tok string) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/events/"+eventID+"/orders",
				strings.NewReader(orderBody(ga, 3)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+tok)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			results <- result{rec.Code, rec.Body.String(), tok}
		}(tok)
	}
	wg.Wait()
	close(results)
	winners, losers := 0, 0
	winTok := ""
	for r := range results {
		switch r.code {
		case http.StatusCreated:
			winners++
			winTok = r.tok
		case http.StatusConflict:
			losers++
		default:
			t.Fatalf("race: unexpected code %d: %s", r.code, r.body)
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("oversell race: want exactly 1 win+1 conflict, got %d/%d", winners, losers)
	}

	// The winner can cancel, releasing inventory, and a fresh 3-ticket order fits.
	findMine := func(tok string) orderWire {
		rec := doJSON(t, h, http.MethodGet, "/api/v1/me/orders", "", tok)
		if rec.Code != http.StatusOK {
			t.Fatalf("my orders: expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var env paginateResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode my orders: %v", err)
		}
		if len(env.Data) == 0 {
			t.Fatalf("no orders for %s", tok)
		}
		raw, _ := json.Marshal(env.Data[0])
		var o orderWire
		_ = json.Unmarshal(raw, &o)
		return o
	}
	winOrder := findMine(winTok)

	// A pending order cannot be re-cancelled twice or cancelled by others.
	if rec := doJSON(t, h, http.MethodPost, "/api/v1/orders/"+winOrder.ID+"/cancel", "", winTok); rec.Code != http.StatusOK {
		t.Fatalf("cancel: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, h, http.MethodPost, "/api/v1/orders/"+winOrder.ID+"/cancel", "", winTok); rec.Code != http.StatusConflict {
		t.Fatalf("double cancel: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, h, http.MethodPost, "/api/v1/events/"+eventID+"/orders", orderBody(ga, 3), buyers[1])
	if rec.Code != http.StatusCreated {
		t.Fatalf("repurchase after cancel: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	reordered := decodeOrder(t, rec)

	// A paid order is not cancellable.
	if _, _, err := serviceOrderService(t, app).ConfirmPaid(context.Background(), reordered.ID, "chapa", "chp-test-2"); err != nil {
		t.Fatalf("confirm reordered: %v", err)
	}
	if rec := doJSON(t, h, http.MethodPost, "/api/v1/orders/"+reordered.ID+"/cancel", "", buyers[1]); rec.Code != http.StatusConflict {
		t.Fatalf("cancel paid order: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}
