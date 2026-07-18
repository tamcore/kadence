package testutil

import (
	"context"
	"testing"
)

func TestSetupTestDBMigratesSchema(t *testing.T) {
	pool := SetupTestDB(t)
	CleanTables(t, pool)

	var n int
	if err := pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		t.Fatalf("query users: %v", err)
	}
	if n != 0 {
		t.Fatalf("users count = %d, want 0 after clean", n)
	}
	if err := pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM sessions").Scan(&n); err != nil {
		t.Fatalf("query sessions: %v", err)
	}
}
