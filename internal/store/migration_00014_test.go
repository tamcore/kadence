package store_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/tamcore/kadence/internal/store/migrations"
)

func TestMigration00014RoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test (needs Docker) in -short mode")
	}
	ctx := context.Background()
	container, err := postgres.Run(ctx, "pgvector/pgvector:pg17",
		postgres.WithDatabase("kadence_test"),
		postgres.WithUsername("kadence"),
		postgres.WithPassword("kadence"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate container: %v", err)
		}
	})
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open sql db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("apply 00014: %v", err)
	}
	assertScheduledSchema(t, ctx, db, true)
	if err := goose.DownToContext(ctx, db, ".", 13); err != nil {
		t.Fatalf("reverse 00014: %v", err)
	}
	assertScheduledSchema(t, ctx, db, false)
	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("reapply 00014: %v", err)
	}
	assertScheduledSchema(t, ctx, db, true)
}

func assertScheduledSchema(t *testing.T, ctx context.Context, db *sql.DB, wantPresent bool) {
	t.Helper()
	const scheduledTasksTable = "scheduled_tasks"
	for _, name := range []string{scheduledTasksTable, "scheduled_task_runs"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", name, err)
		}
		if exists != wantPresent {
			t.Fatalf("table %s exists=%t, want %t", name, exists, wantPresent)
		}
	}
	for _, column := range []struct {
		table string
		name  string
	}{
		{table: "users", name: "timezone"},
		{table: "conversations", name: "kind"},
		{table: scheduledTasksTable, name: "delivery_policy"},
		{table: scheduledTasksTable, name: "initial_run"},
		{table: scheduledTasksTable, name: "stop_condition"},
		{table: scheduledTasksTable, name: "static_message"},
	} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM information_schema.columns WHERE table_name = $1 AND column_name = $2
		)`, column.table, column.name).Scan(&exists); err != nil {
			t.Fatalf("check column %s.%s: %v", column.table, column.name, err)
		}
		if exists != wantPresent {
			t.Fatalf("column %s.%s exists=%t, want %t", column.table, column.name, exists, wantPresent)
		}
	}
}
