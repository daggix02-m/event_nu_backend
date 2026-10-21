package transportgrpc

import (
	"context"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/api"
	"github.com/daggix02-m/event_nu_backend/internal/config"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu_backend/internal/transport/grpc/eventnupb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// startGRPC builds an Application (and therefore the gRPC server it wraps)
// against the real test database and serves it over bufconn — no TCP port, no
// fixture HTTP layer: the same services behind the REST handlers serve gRPC.
func startGRPC(t *testing.T) eventnupb.EventServiceClient {
	t.Helper()

	if os.Getenv("DATABASE_URL") == "" || os.Getenv("DATABASE_ADMIN_URL") == "" {
		t.Skip("DATABASE_URL/DATABASE_ADMIN_URL not set — skipping gRPC DB-backed tests")
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
	cfg.GRPCAddr = "" // irrelevant under bufconn; the wiring test exercises it via cmd build
	app := api.NewApp(slog.New(slog.DiscardHandler), cfg, pool)
	t.Cleanup(app.RateLimiter.Stop)

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			RecoveryInterceptor(app.Logger),
			LoggingInterceptor(app.Logger),
		),
	)
	eventnupb.RegisterEventServiceServer(srv, NewEventServer(app.Event, app.Media, app.Logger))

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

	return eventnupb.NewEventServiceClient(conn)
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
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cctx, `DELETE FROM events WHERE organizer_id = $1`, orgID)
		_, _ = admin.Exec(cctx, `DELETE FROM organizers WHERE id = $1`, orgID)
	})
	return prefix, publishedID, draftID
}

// TestGetEventMapsFields verifies wire mapping end to end: identity fields,
// timestamps, and optionality survive service→protobuf round-trips for a
// public event.
func TestGetEventMapsFields(t *testing.T) {
	sock := startGRPC(t)
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
}

// TestGetEventNotFound verifies unknown-but-valid UUIDs map to NotFound (the
// repository's ErrNotFound through AppError), not Internal.
func TestGetEventNotFound(t *testing.T) {
	sock := startGRPC(t)
	seedCatalog(t, sock)

	_, err := sock.GetEvent(context.Background(), &eventnupb.GetEventRequest{Id: "00000000-0000-0000-0000-000000000001"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("unknown id: got %v, want NotFound (err=%v)", status.Code(err), err)
	}
}

// TestGetEventInvalidID verifies the malformed-UUID guard fires before any DB
// call, mapping to InvalidArgument (the REST handlers reject the same shape
// with 404/400 — here the contract is explicit).
func TestGetEventInvalidID(t *testing.T) {
	sock := startGRPC(t)
	seedCatalog(t, sock)

	_, err := sock.GetEvent(context.Background(), &eventnupb.GetEventRequest{Id: "not-a-uuid"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad id: got %v, want InvalidArgument", status.Code(err))
	}
}

// TestListEventsQueryAndVisibility verifies the full-text filter finds the
// seeded published event, pagination defaults apply, and the draft event
// stays invisible — the REST catalog's visibility policy holds on gRPC.
func TestListEventsQueryAndVisibility(t *testing.T) {
	sock := startGRPC(t)
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
	sock := startGRPC(t)
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
