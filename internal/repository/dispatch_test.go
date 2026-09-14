package repository

import (
	"context"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedNotification(t *testing.T, pool *pgxpool.Pool, userID string) string {
	t.Helper()
	notifs := NewNotificationRepository(pool)
	id, err := notifs.Notify(context.Background(), userID, "system", "Dispatch test", "body", map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("seed notification: %v", err)
	}
	return id
}

// contains reports whether the claimed batch includes the target notification.
func containsNotification(claimed []*domain.Notification, id string) *domain.Notification {
	for _, n := range claimed {
		if n.ID == id {
			return n
		}
	}
	return nil
}

// TestClaimForDispatchLeaseRecovery: claiming leases rows (a second claim sees
// nothing), an expired lease makes them claimable again, and marking dispatched
// / failed removes them from the queue. Runs against the shared test DB, so
// assertions are containment-based (other rows may be queued too).
func TestClaimForDispatchLeaseRecovery(t *testing.T) {
	pool := rlsServicePool(t)
	uid := seedRLSUserNamed(t, pool, "dspn")

	notifs := NewNotificationRepository(pool)
	nid := seedNotification(t, pool, uid)

	// First claim leases the row and returns it.
	claimed, err := notifs.ClaimForDispatch(context.Background(), 10000, time.Minute)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	got := containsNotification(claimed, nid)
	if got == nil {
		t.Fatalf("first claim must include the seeded notification")
	}
	if got.DispatchAttempts != 0 {
		t.Fatalf("fresh notification must start at 0 attempts, got %d", got.DispatchAttempts)
	}

	// Leased: it must not appear again.
	if again, err := notifs.ClaimForDispatch(context.Background(), 10000, time.Minute); err != nil {
		t.Fatalf("second claim: %v", err)
	} else if containsNotification(again, nid) != nil {
		t.Fatalf("leased row must not be re-claimed")
	}

	// Crash simulation: lease expires, reclaim succeeds.
	if _, err := pool.Exec(context.Background(),
		`UPDATE notifications SET dispatch_lease_at = now() - interval '1 hour' WHERE id = $1`, nid); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	if again, err := notifs.ClaimForDispatch(context.Background(), 10000, time.Minute); err != nil {
		t.Fatalf("reclaim after expiry: %v", err)
	} else if containsNotification(again, nid) == nil {
		t.Fatalf("expired lease must be reclaimable")
	}

	// Backoff: not due yet → not claimed; due again → claimed with +1 attempt.
	if err := notifs.MarkDispatchFailed(context.Background(), nid, 1, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if again, err := notifs.ClaimForDispatch(context.Background(), 10000, time.Minute); err != nil {
		t.Fatalf("claim while backed off: %v", err)
	} else if containsNotification(again, nid) != nil {
		t.Fatalf("backed-off row must not be claimed early")
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE notifications SET next_dispatch_at = now() - interval '1 second' WHERE id = $1`, nid); err != nil {
		t.Fatalf("make due: %v", err)
	}
	if again, err := notifs.ClaimForDispatch(context.Background(), 10000, time.Minute); err != nil {
		t.Fatalf("claim after backoff: %v", err)
	} else if retried := containsNotification(again, nid); retried == nil || retried.DispatchAttempts != 1 {
		t.Fatalf("row must be retried with attempt 1, got %+v", retried)
	}

	// Marked dispatched: gone from the queue.
	if err := notifs.MarkDispatched(context.Background(), nid); err != nil {
		t.Fatalf("mark dispatched: %v", err)
	}
	if again, err := notifs.ClaimForDispatch(context.Background(), 10000, time.Minute); err != nil {
		t.Fatalf("claim after dispatched: %v", err)
	} else if containsNotification(again, nid) != nil {
		t.Fatalf("dispatched row must not be re-claimed")
	}
}

func containsReminder(claimed []*domain.Reminder, id string) bool {
	for _, r := range claimed {
		if r.ID == id {
			return true
		}
	}
	return false
}

// TestClaimDueReminders covers the reminder half of the queue: due reminders
// are claimed once (leased), and marking them sent/failed removes them.
func TestClaimDueReminders(t *testing.T) {
	pool := rlsServicePool(t)
	uid := seedRLSUserNamed(t, pool, "dsprem")

	var eventID string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM events ORDER BY created_at LIMIT 1`).Scan(&eventID); err != nil {
		t.Skipf("no events to attach reminders to: %v", err)
	}

	insertReminder := func(t *testing.T, remindAt time.Time) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(context.Background(), `
			INSERT INTO event_reminders (event_id, user_id, remind_at)
			VALUES ($1::uuid, $2::uuid, $3)
			RETURNING id::text`, eventID, uid, remindAt).Scan(&id); err != nil {
			t.Fatalf("insert reminder: %v", err)
		}
		return id
	}

	reminders := NewReminderRepository(pool)
	rid := insertReminder(t, time.Now().Add(-time.Minute))

	claimed, err := reminders.ClaimDue(context.Background(), 10000, time.Minute)
	if err != nil {
		t.Fatalf("claim due reminders: %v", err)
	}
	if !containsReminder(claimed, rid) {
		t.Fatalf("claim must include the due reminder")
	}

	// Lease still active → cannot appear again.
	if again, err := reminders.ClaimDue(context.Background(), 10000, time.Minute); err != nil {
		t.Fatalf("re-claim while leased: %v", err)
	} else if containsReminder(again, rid) {
		t.Fatalf("leased reminder must not be re-claimed")
	}

	// Sent: no longer claimable.
	nid := seedNotification(t, pool, uid)
	if err := reminders.MarkReminderSent(context.Background(), rid, nid); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	if again, err := reminders.ClaimDue(context.Background(), 10000, time.Minute); err != nil {
		t.Fatalf("claim after sent: %v", err)
	} else if containsReminder(again, rid) {
		t.Fatalf("sent reminder must not be re-claimed")
	}
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM event_reminders WHERE id = $1::uuid`, rid).Scan(&status); err != nil {
		t.Fatalf("read reminder status: %v", err)
	}
	if status != "sent" {
		t.Fatalf("expected status 'sent', got %q", status)
	}

	// Failed: no longer claimable, status failed.
	rid2 := insertReminder(t, time.Now().Add(-time.Minute))
	if _, err := reminders.ClaimDue(context.Background(), 10000, time.Minute); err != nil {
		t.Fatalf("claim second reminder: %v", err)
	}
	if err := reminders.MarkReminderFailed(context.Background(), rid2); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT status FROM event_reminders WHERE id = $1::uuid`, rid2).Scan(&status); err != nil {
		t.Fatalf("read second reminder status: %v", err)
	}
	if status != "failed" {
		t.Fatalf("expected status 'failed', got %q", status)
	}
}
