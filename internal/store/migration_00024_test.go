package store_test

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx database/sql driver
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/tamcore/kadence/internal/store/migrations"
	"github.com/tamcore/kadence/internal/store/testutil"
)

func TestMCPAuditIntentGuardMigrationReverses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test (needs Docker) in -short mode")
	}
	ctx := t.Context()
	container, err := postgres.Run(ctx, testutil.PostgresImage,
		postgres.WithDatabase("kadence_test"),
		postgres.WithUsername("kadence"),
		postgres.WithPassword("kadence"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(ctx, db, ".", 24); err != nil {
		t.Fatalf("apply migrations through 24: %v", err)
	}

	started := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	var id int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO mcp_call_audit (
			actor_user_id, actor_username, conversation_id, source, model, tool_call_id,
			tool_name, arguments, intent, guard_verdict, guard_reason, status, started_at, finished_at
		) VALUES (7, 'alice', '11111111-1111-4111-8111-111111111111', 'chat', 'coach-model', 'call-blocked',
			'weather__forecast', '{}', 'Show weather', 'denied', 'Tool mutates data', 'blocked', $1, $1)
		RETURNING id`, started).Scan(&id); err != nil {
		t.Fatalf("insert blocked audit: %v", err)
	}
	if err := goose.DownToContext(ctx, db, ".", 23); err != nil {
		t.Fatalf("reverse migration 24: %v", err)
	}

	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM mcp_call_audit WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("get reversed audit: %v", err)
	}
	if status != "failed" {
		t.Fatalf("status after reverse = %q, want failed", status)
	}
	var columns int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*)
		  FROM information_schema.columns
		 WHERE table_name = 'mcp_call_audit'
		   AND column_name IN ('intent', 'guard_verdict', 'guard_reason')`).Scan(&columns); err != nil {
		t.Fatalf("count guard columns: %v", err)
	}
	if columns != 0 {
		t.Fatalf("guard columns remain after reverse: %d", columns)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO mcp_call_audit (
			actor_user_id, actor_username, conversation_id, source, model, tool_call_id,
			tool_name, arguments, status, started_at
		) VALUES (8, 'bob', '22222222-2222-4222-8222-222222222222', 'chat', 'coach-model', 'call-blocked-reject',
			'weather__forecast', '{}', 'blocked', $1)`, started); err == nil {
		t.Fatal("insert blocked audit after reverse = nil error, want status constraint rejection")
	}
}
