package testutil

import (
	"testing"
)

func TestSetupTestDBMigratesSchema(t *testing.T) {
	pool := SetupTestDB(t)

	var n int
	if err := pool.QueryRow(t.Context(), "SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		t.Fatalf("query users: %v", err)
	}
	if n != 0 {
		t.Fatalf("users count = %d, want 0 after clean", n)
	}
	if err := pool.QueryRow(t.Context(), "SELECT COUNT(*) FROM sessions").Scan(&n); err != nil {
		t.Fatalf("query sessions: %v", err)
	}
}

// TestSetupTestDBTruncatesLeftoverRows is the guarantee that lets every caller
// drop its own explicit truncate: a later setup must not see earlier rows.
func TestSetupTestDBTruncatesLeftoverRows(t *testing.T) {
	pool := SetupTestDB(t)
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO users (username, email, password_hash, role)
		 VALUES ('leftover', 'leftover@example.test', 'x', 'user')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	pool = SetupTestDB(t)

	var n int
	if err := pool.QueryRow(t.Context(), "SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		t.Fatalf("query users: %v", err)
	}
	if n != 0 {
		t.Fatalf("users count = %d, want 0 after re-setup", n)
	}
}
