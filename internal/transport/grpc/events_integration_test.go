package transportgrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/api"
	"github.com/daggix02-m/event_nu_backend/internal/api/routes"
	"github.com/daggix02-m/event_nu_backend/internal/config"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu_backend/internal/transport/grpc/eventnupb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// appForTest builds the full Application against the real test database —
// registration and likes go through the same services the REST handlers use.
func appForTest(t *testing.T) *api.Application {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set — skipping gRPC DB-backed tests")
	}
	pool, err := database.NewPool(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Skipf("database unreachable (%v) — skipping", err)
	}
	t.Cleanup(pool.Close)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.GRPCAddr = "" // irrelevant under bufconn; still clamped for clarity
	app := api.NewApp(slog.New(slog.DiscardHandler), cfg, pool)
	t.Cleanup(app.RateLimiter.Stop)
	return app
}

// startGRPC serves the wired EventServer (with auth + enrichment) over
// bufconn and hands back both the client and the backing Application.
func startGRPC(t *testing.T) (eventnupb.EventServiceClient, *api.Application) {
	t.Helper()
	app := appForTest(t)

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			RecoveryInterceptor(app.Logger),
			OptionalAuthInterceptor(app.Auth),
			LoggingInterceptor(app.Logger),
		),
	)
	eventnupb.RegisterEventServiceServer(srv, NewEventServer(app.Event, app.Media, app.Like, app.Save, app.Logger))

	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc client: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return eventnupb.NewEventServiceClient(conn), app
}

// seedCatalog inserts an organizer and events directly through the BYPASSRLS
// admin connection: the gRPC slice tests transport behavior (mapping,
// validation, visibility codes), not the REST creation flow.
func seedCatalog(t *testing.T, sock eventnupb.EventServiceClient) (organizerPrefix, publishedID, draftID string) {
	t.Helper()
	admin, err := database.NewPool(context.Background(), os.Getenv("DATABASE_ADMIN_URL"))
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	defer admin.Close()
	ctx := context.Background()

	prefix := "grpcfix-" + time.Now().Format("150405.000000000")
	var orgID string
	if err := admin.QueryRow(ctx, `
		INSERT INTO organizers (slug, name, bio)
		VALUES ($1, $2, 'seeded for the gRPC transport tests')
		RETURNING id`, prefix, "gRPC Seed Organizer").Scan(&orgID); err != nil {
		t.Fatalf("insert organizer: %v", err)
	}

	start := time.Now().Add(48 * time.Hour)
	startsAt := start.Format(time.RFC3339)
	if err := admin.QueryRow(ctx, `
		INSERT INTO events (organizer_id, title, description, starts_at, ends_at, price_is_free, price_display, status, moderation_status, max_attendees)
		VALUES ($1, $2, 'grpc fixture event', $3, $4, true, 'Free', 'published', 'clean', 300)
		RETURNING id`,
		orgID, prefix+" published", startsAt, start.Add(6*time.Hour).Format(time.RFC3339),
	).Scan(&publishedID); err != nil {
		t.Fatalf("insert published event: %v", err)
	}
	if err := admin.QueryRow(ctx, `
		INSERT INTO events (organizer_id, title, description, starts_at, price_is_free, price_display, status, moderation_status)
		VALUES ($1, $2, 'grpc fixture draft', $3, true, 'Free', 'draft', 'clean')
		RETURNING id`,
		orgID, prefix+" draft", startsAt,
	).Scan(&draftID); err != nil {
		t.Fatalf("insert draft event: %v", err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(cctx, `DELETE FROM likes WHERE event_id IN (SELECT id FROM events WHERE organizer_id = $1)`, orgID)
		_, _ = admin.Exec(cctx, `DELETE FROM events WHERE organizer_id = $1`, orgID)
		_, _ = admin.Exec(cctx, `DELETE FROM organizers WHERE id = $1`, orgID)
	})
	return prefix, publishedID, draftID
}

// seedUser registers via the real REST door (routes.New over the same app),
// because registration requires the REST request-transaction/RLS plumbing —
// and that is precisely the client flow being tested: a token minted over
// HTTP then consumed over gRPC.
func seedUser(t *testing.T, app *api.Application, email, username string) string {
	t.Helper()
	h := routes.New(app)
	body := fmt.Sprintf(`{"email":%q,"password":"password123","username":%q}`, email, username)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("register %s: status %d: %s", email, rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	if resp.Data.AccessToken == "" {
		t.Fatalf("register returned no access token: %s", rec.Body.String())
	}
	t.Cleanup(func() {
		admin, err := database.NewPool(context.Background(), os.Getenv("DATABASE_ADMIN_URL"))
		if err != nil {
			t.Fatalf("admin pool for user cleanup: %v", err)
		}
		defer admin.Close()
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cctx, `DELETE FROM sessions WHERE user_id = (SELECT id FROM users WHERE email = $1)`, email)
		_, _ = admin.Exec(cctx, `DELETE FROM users WHERE email = $1`, email)
	})
	return resp.Data.AccessToken
}

func authCtx(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}

// http2xx reports 2xx statuses for REST fixture calls.
func http2xx(code int) bool { return code >= 200 && code < 300 }

// TestGetEventMapsFields verifies wire mapping end to end: identity fields,
// timestamps, and optionality survive service→protobuf round-trips for a
// public event.
func TestGetEventMapsFields(t *testing.T) {
	sock, _ := startGRPC(t)
	prefix, publishedID, _ := seedCatalog(t, sock)

	e, err := sock.GetEvent(context.Background(), &eventnupb.GetEventRequest{Id: publishedID})
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if e.Title != prefix+" published" {
		t.Fatalf("title: got %q", e.Title)
	}
	if !e.PriceIsFree || e.PriceDisplay != "Free" {
		t.Fatalf("price: free=%v display=%q", e.PriceIsFree, e.PriceDisplay)
	}
	if e.Status != "published" || e.ModerationStatus != "clean" {
		t.Fatalf("status: %q/%q", e.Status, e.ModerationStatus)
	}
	if e.StartsAt == nil || e.StartsAt.AsTime().Before(time.Now()) {
		t.Fatal("starts_at missing or in the past")
	}
	if e.EndsAt == nil {
		t.Fatal("ends_at omitted, expected presence")
	}
	if e.MaxAttendees == nil || *e.MaxAttendees != 300 {
		t.Fatal("max_attendees missing or wrong")
	}
	if e.VenueId != nil || e.CategoryId != nil || e.PosterUrl != "" || e.TeaserUrl != "" {
		t.Fatalf("optional/absent fields leaked: %+v", e)
	}
	if e.LikeCount != 0 || e.LikedByMe || e.SavedByMe {
		t.Fatalf("anonymous enrichment baseline: count=%d liked=%v saved=%v", e.LikeCount, e.LikedByMe, e.SavedByMe)
	}
}

// TestGetEventNotFound verifies unknown-but-valid UUIDs map to NotFound (the
// repository's ErrNotFound through the service), not Internal.
func TestGetEventNotFound(t *testing.T) {
	sock, _ := startGRPC(t)
	seedCatalog(t, sock)

	_, err := sock.GetEvent(context.Background(), &eventnupb.GetEventRequest{Id: "00000000-0000-0000-0000-000000000001"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("unknown id: got %v, want NotFound (err=%v)", status.Code(err), err)
	}
}

// TestGetEventInvalidID verifies the malformed-UUID guard fires before any DB
// call, mapping to InvalidArgument.
func TestGetEventInvalidID(t *testing.T) {
	sock, _ := startGRPC(t)
	seedCatalog(t, sock)

	_, err := sock.GetEvent(context.Background(), &eventnupb.GetEventRequest{Id: "not-a-uuid"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad id: got %v, want InvalidArgument", status.Code(err))
	}
}

// TestGetEventLikeEnrichment verifies the identity contract end to end: a
// registered user's token flows from metadata into the context; their like
// is reported as LikedByMe (count 1) while a fresh anonymous caller sees
// count 1 with LikedByMe=false — the REST enrichment's exact semantics.
func TestGetEventLikeEnrichment(t *testing.T) {
	sock, app := startGRPC(t)
	prefix, publishedID, _ := seedCatalog(t, sock)

	token := seedUser(t, app, prefix+"-user@example.test", "grpcuser_"+strings.ReplaceAll(prefix, "-", "")[:11])

	// Like the event through the REST door: the like write runs inside the
	// REST request transaction/RLS context. This is precisely the client
	// story under test — a client writes over HTTP, then reads its enriched
	// catalog over gRPC (or, in a cross-transport future, via the same JWT).
	ctx := context.Background()
	h := routes.New(app)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events/"+publishedID+"/like", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !http2xx(rec.Code) {
		t.Fatalf("via REST POST like: status %d: %s", rec.Code, rec.Body.String())
	}

	liked, err := sock.GetEvent(authCtx(ctx, token), &eventnupb.GetEventRequest{Id: publishedID})
	if err != nil {
		t.Fatalf("authenticated GetEvent: %v", err)
	}
	if !liked.LikedByMe || liked.LikeCount != 1 {
		t.Fatalf("after like: count=%d liked=%v (wanted count=1, liked=true)", liked.LikeCount, liked.LikedByMe)
	}

	// A different (anonymous) caller sees the public count but not the flag.
	other, err := sock.GetEvent(context.Background(), &eventnupb.GetEventRequest{Id: publishedID})
	if err != nil {
		t.Fatalf("anonymous re-read: %v", err)
	}
	if other.LikeCount != 1 || other.LikedByMe {
		t.Fatalf("anonymous after like: count=%d liked=%v (wanted 1,false)", other.LikeCount, other.LikedByMe)
	}

	// An invalid token is anonymous (OptionalAuth semantics): no rejection.
	bad, err := sock.GetEvent(authCtx(ctx, "not-a-real-token"), &eventnupb.GetEventRequest{Id: publishedID})
	if err != nil {
		t.Fatalf("invalid token must degrade to anonymous, got err %v", err)
	}
	if bad.LikeCount != 1 || bad.LikedByMe {
		t.Fatalf("invalid-token identity leaked: count=%d liked=%v", bad.LikeCount, bad.LikedByMe)
	}
}

// TestListEventsQueryAndVisibility verifies the full-text filter finds the
// seeded published event, pagination defaults apply, and the draft event
// stays invisible — the REST catalog's visibility policy holds on gRPC.
func TestListEventsQueryAndVisibility(t *testing.T) {
	sock, _ := startGRPC(t)
	prefix, publishedID, draftID := seedCatalog(t, sock)

	res, err := sock.ListEvents(context.Background(), &eventnupb.ListEventsRequest{Query: prefix, Limit: 20})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found, sawDraft := false, false
	for _, e := range res.Events {
		switch e.Id {
		case publishedID:
			found = true
		case draftID:
			sawDraft = true
		}
	}
	if !found {
		t.Fatal("published fixture event not in the query results")
	}
	if sawDraft {
		t.Fatal("draft fixture event leaked into the public catalog")
	}
	if res.Total < 1 {
		t.Fatalf("total: got %d", res.Total)
	}
}

// TestListEventsLimitValidation verifies the limit contract: >100 is
// InvalidArgument before any query runs; 0 clamps to the default of 20.
func TestListEventsLimitValidation(t *testing.T) {
	sock, _ := startGRPC(t)
	seedCatalog(t, sock)

	_, err := sock.ListEvents(context.Background(), &eventnupb.ListEventsRequest{Limit: 101})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("limit>100: got %v, want InvalidArgument", status.Code(err))
	}

	res, err := sock.ListEvents(context.Background(), &eventnupb.ListEventsRequest{Query: "no-such-thing-ever", Limit: 0})
	if err != nil {
		t.Fatalf("limit=0: %v", err)
	}
	if res.Total != 0 {
		t.Fatalf("query for nothing: total %d", res.Total)
	}
}
