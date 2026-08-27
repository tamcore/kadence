package store_test

import (
	"testing"

	"github.com/tamcore/kadence/internal/store/testutil"
)

func TestMigration00027MCPOAuthTables(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	pool := testutil.SetupTestDB(t)
	ctx := t.Context()

	for _, table := range []string{"mcp_oauth_tokens", "mcp_oauth_transactions"} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`,
			table).Scan(&exists); err != nil {
			t.Fatalf("query %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("table %s does not exist", table)
		}
	}

	var userID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash)
		 VALUES ('oauthmig','oauthmig@example.invalid','x') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	const insert = `INSERT INTO mcp_oauth_tokens
		    (user_id, server_id, access_token, access_expires_at, refresh_token, scope, resource)
		 VALUES ($1,'garmin','\x01','2026-08-17T10:00:00Z','\x02','garmin:read','https://h.invalid/mcp')`

	if _, err := pool.Exec(ctx, insert, userID); err != nil {
		t.Fatalf("insert token: %v", err)
	}
	if _, err := pool.Exec(ctx, insert, userID); err == nil {
		t.Fatal("a second row for the same (user, server) was accepted, want a unique violation")
	}

	if _, err := pool.Exec(ctx,
		`UPDATE mcp_oauth_tokens SET status = 'nonsense' WHERE user_id = $1`, userID); err == nil {
		t.Fatal("an unknown status was accepted, want a check violation")
	}

	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	var left int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM mcp_oauth_tokens WHERE user_id = $1`, userID).Scan(&left); err != nil {
		t.Fatalf("count after delete: %v", err)
	}
	if left != 0 {
		t.Fatalf("%d token rows survived the user, want 0 (cascade)", left)
	}
}
