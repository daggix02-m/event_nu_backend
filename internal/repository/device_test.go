package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/infrastructure/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedRLSUserNamed(t *testing.T, pool *pgxpool.Pool, name string) string {
	t.Helper()
	users := NewUserRepository(pool)
	email := fmt.Sprintf("%s+%d@test.example", name, time.Now().UnixNano())
	user, err := users.Create(context.Background(), email, "x", name+fmt.Sprint(time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("seed user %s: %v", name, err)
	}
	return user.ID
}

// userContext opens a transaction carrying the RLS identity of the given user
// on the app_user pool. READ COMMITTED snapshots are per-statement, so a later
// statement on the tx sees changes committed elsewhere — but not writes still
// uncommitted on other connections.
func userContext(t *testing.T, pool *pgxpool.Pool, userID string) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	rctx := database.ContextWithTx(ctx, tx)
	if err := database.SetRLSContextTx(rctx, tx, userID, "user"); err != nil {
		t.Fatalf("set rls context: %v", err)
	}
	return tx
}

// listOwned is a tiny helper to read the caller's devices on a fresh identity.
func listOwned(t *testing.T, pool *pgxpool.Pool, devices *DeviceRepository, userID string) []*domain.Device {
	t.Helper()
	tx := userContext(t, pool, userID)
	defer tx.Rollback(context.Background())
	list, err := devices.ListByUser(database.ContextWithTx(context.Background(), tx), userID)
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	return list
}

// TestDeviceRLSRegistrationAndTokenHandover: a user registers a token, sees it
// under their own RLS identity, and another user signing in on the same phone
// (same token) takes ownership of the row — while the original owner no longer
// sees it. The reassignment crosses an RLS boundary, which is why registration
// runs through the SECURITY DEFINER upsert_user_device.
func TestDeviceRLSRegistrationAndTokenHandover(t *testing.T) {
	pool := rlsTestPool(t)
	seedPool := rlsServicePool(t)

	aID := seedRLSUserNamed(t, seedPool, "deva")
	bID := seedRLSUserNamed(t, seedPool, "devb")
	token := fmt.Sprintf("fcm-token-%d", time.Now().UnixNano())

	devices := NewDeviceRepository(pool)

	// — A registers the token —
	txA := userContext(t, pool, aID)
	defer txA.Rollback(context.Background())
	ctxA := database.ContextWithTx(context.Background(), txA)

	dev, err := devices.Upsert(ctxA, aID, token, "android")
	if err != nil {
		t.Fatalf("A upsert device: %v", err)
	}
	if dev.UserID != aID {
		t.Fatalf("device must be owned by A, got %q", dev.UserID)
	}
	txA.Commit(context.Background())

	if got := listOwned(t, pool, devices, aID); len(got) != 1 {
		t.Fatalf("A must see exactly 1 device, got %d", len(got))
	}

	// — B cannot see A's device —
	if got := listOwned(t, pool, devices, bID); len(got) != 0 {
		t.Fatalf("B must not see A's device, got %d", len(got))
	}

	// — B signs in on the same phone: token reassigned to B (own tx, committed) —
	txB := userContext(t, pool, bID)
	ctxB := database.ContextWithTx(context.Background(), txB)

	devB, err := devices.Upsert(ctxB, bID, token, "ios")
	if err != nil {
		t.Fatalf("B upsert same token: %v", err)
	}
	if devB.ID != dev.ID {
		t.Fatalf("token handover must reuse the same device row, got %q vs %q", devB.ID, dev.ID)
	}
	if devB.UserID != bID {
		t.Fatalf("token must now be owned by B, got %q", devB.UserID)
	}
	if devB.Platform != "ios" {
		t.Fatalf("platform must reflect B's registration, got %q", devB.Platform)
	}
	txB.Commit(context.Background())

	if got := listOwned(t, pool, devices, bID); len(got) != 1 {
		t.Fatalf("B must see exactly 1 device after handover, got %d", len(got))
	}
	if got := listOwned(t, pool, devices, aID); len(got) != 0 {
		t.Fatalf("A must no longer see the token after handover, got %d", len(got))
	}
}

// TestDeviceCrossUserDeleteCantRemoveOthers: deregistering by id must only
// ever remove the caller's own row — another user's device id is a no-op.
func TestDeviceCrossUserDeleteCantRemoveOthers(t *testing.T) {
	pool := rlsTestPool(t)
	seedPool := rlsServicePool(t)

	aID := seedRLSUserNamed(t, seedPool, "devx")
	bID := seedRLSUserNamed(t, seedPool, "devy")
	token := fmt.Sprintf("fcm-token-x-%d", time.Now().UnixNano())

	devices := NewDeviceRepository(pool)

	txA := userContext(t, pool, aID)
	defer txA.Rollback(context.Background())
	ctxA := database.ContextWithTx(context.Background(), txA)

	dev, err := devices.Upsert(ctxA, aID, token, "android")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	txA.Commit(context.Background())

	// B tries to delete A's device id — must not remove it.
	txB := userContext(t, pool, bID)
	defer txB.Rollback(context.Background())
	ctxB := database.ContextWithTx(context.Background(), txB)
	if err := devices.Delete(ctxB, bID, dev.ID); err != nil {
		t.Fatalf("B delete (expected no-op): %v", err)
	}
	txB.Commit(context.Background())

	if got := listOwned(t, pool, devices, aID); len(got) != 1 {
		t.Fatalf("A's device must survive B's delete attempt, got %d", len(got))
	}

	// A deletes their own device — gone.
	txC := userContext(t, pool, aID)
	defer txC.Rollback(context.Background())
	if err := devices.Delete(database.ContextWithTx(context.Background(), txC), aID, dev.ID); err != nil {
		t.Fatalf("A delete: %v", err)
	}
	txC.Commit(context.Background())

	if got := listOwned(t, pool, devices, aID); len(got) != 0 {
		t.Fatalf("A's device must be gone after delete, got %d", len(got))
	}
}

// TestDevicePruneByToken: the push consumer prunes invalid tokens via the
// service role; the row disappears for everyone.
func TestDevicePruneByToken(t *testing.T) {
	pool := rlsTestPool(t)
	seedPool := rlsServicePool(t)

	aID := seedRLSUserNamed(t, seedPool, "devp")
	token := fmt.Sprintf("fcm-token-p-%d", time.Now().UnixNano())

	devices := NewDeviceRepository(pool)

	txA := userContext(t, pool, aID)
	defer txA.Rollback(context.Background())
	ctxA := database.ContextWithTx(context.Background(), txA)
	if _, err := devices.Upsert(ctxA, aID, token, "android"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	txA.Commit(context.Background())

	if got := listOwned(t, pool, devices, aID); len(got) != 1 {
		t.Fatalf("A must own 1 device before prune, got %d", len(got))
	}

	service := NewDeviceRepository(seedPool)
	if err := service.DeleteByToken(context.Background(), token); err != nil {
		t.Fatalf("prune by token: %v", err)
	}

	if got := listOwned(t, pool, devices, aID); len(got) != 0 {
		t.Fatalf("device must be pruned, got %d", len(got))
	}
}
