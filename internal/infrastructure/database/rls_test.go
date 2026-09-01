package database

import (
	"context"
	"os"
	"testing"
)

func TestServiceRolePreset(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("no DATABASE_URL")
	}
	pool, err := NewPoolWithRole(context.Background(), dsn, "service")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	var role string
	var priv bool
	if err := pool.QueryRow(context.Background(), `SELECT current_app_role(), is_privileged()`).Scan(&role, &priv); err != nil {
		t.Fatalf("query: %v", err)
	}
	t.Logf("role=%q is_privileged=%v", role, priv)
	if role != "service" || !priv {
		t.Fatalf("expected service/true, got %q/%v", role, priv)
	}
}