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

func TestScheduledMigrationsRoundTrip(t *testing.T) {
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
	if err := goose.UpToContext(ctx, db, ".", 14); err != nil {
		t.Fatalf("apply migration 14: %v", err)
	}
	assertScheduledSchema(t, ctx, db, true)
	assertMessagePurposeColumn(t, ctx, db, false)
	insertLegacyScheduledMessages(t, ctx, db)
	if err := goose.UpToContext(ctx, db, ".", 15); err != nil {
		t.Fatalf("apply migration 15: %v", err)
	}
	assertMessagePurposeColumn(t, ctx, db, true)
	assertLegacyScheduledMessagePurposes(t, ctx, db)
	if err := goose.DownToContext(ctx, db, ".", 14); err != nil {
		t.Fatalf("reverse migration 15: %v", err)
	}
	assertScheduledSchema(t, ctx, db, true)
	assertMessagePurposeColumn(t, ctx, db, false)
	if err := goose.UpToContext(ctx, db, ".", 15); err != nil {
		t.Fatalf("reapply migration 15: %v", err)
	}
	assertLegacyScheduledMessagePurposes(t, ctx, db)
	if err := goose.DownToContext(ctx, db, ".", 13); err != nil {
		t.Fatalf("reverse scheduled migrations: %v", err)
	}
	assertScheduledSchema(t, ctx, db, false)
	assertMessagePurposeColumn(t, ctx, db, false)
	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("reapply scheduled migrations: %v", err)
	}
	assertScheduledSchema(t, ctx, db, true)
	assertMessagePurposeColumn(t, ctx, db, true)
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

func assertMessagePurposeColumn(t *testing.T, ctx context.Context, db *sql.DB, wantPresent bool) {
	t.Helper()
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		 WHERE table_name = 'messages' AND column_name = 'purpose'
	)`).Scan(&exists); err != nil {
		t.Fatalf("check messages.purpose: %v", err)
	}
	if exists != wantPresent {
		t.Fatalf("messages.purpose exists=%t, want %t", exists, wantPresent)
	}
}

func insertLegacyScheduledMessages(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var userID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (username, email, password_hash, role)
		 VALUES ('legacy-scheduled', 'legacy-scheduled@example.com', 'hash', 'user')
		 RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert legacy user: %v", err)
	}
	var conversationID, taskID string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO conversations (user_id, title, kind)
		 VALUES ($1, 'Legacy Scheduled', 'scheduled')
		 RETURNING id::text`, userID).Scan(&conversationID); err != nil {
		t.Fatalf("insert legacy conversation: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO scheduled_tasks (user_id, conversation_id, kind, state, timezone)
		 VALUES ($1, $2::uuid, 'data', 'active', 'UTC')
		 RETURNING id::text`, userID, conversationID).Scan(&taskID); err != nil {
		t.Fatalf("insert legacy task: %v", err)
	}
	deliveryAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO scheduled_task_runs (
		     task_id, occurrence_key, scheduled_for, state, finished_at, result, unread
		 ) VALUES ($1::uuid, 'legacy-delivery', $2, 'delivered', $2, 'same text', TRUE)`,
		taskID, deliveryAt); err != nil {
		t.Fatalf("insert legacy run: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO messages (conversation_id, role, content, created_at) VALUES
		 ($1::uuid, 'user', 'define me', $2),
		 ($1::uuid, 'assistant', 'same text', $3),
		 ($1::uuid, 'assistant', 'same text', $4)`,
		conversationID, deliveryAt.Add(-2*time.Minute), deliveryAt.Add(-time.Minute), deliveryAt); err != nil {
		t.Fatalf("insert legacy messages: %v", err)
	}
}

func assertLegacyScheduledMessagePurposes(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(ctx,
		`SELECT purpose FROM messages
		  WHERE content IN ('define me', 'same text')
		  ORDER BY id`)
	if err != nil {
		t.Fatalf("read legacy purposes: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var purposes []string
	for rows.Next() {
		var purpose string
		if err := rows.Scan(&purpose); err != nil {
			t.Fatalf("scan legacy purpose: %v", err)
		}
		purposes = append(purposes, purpose)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate legacy purposes: %v", err)
	}
	want := []string{"scheduled_definition", "scheduled_definition", "scheduled_delivery"}
	if len(purposes) != len(want) {
		t.Fatalf("legacy purposes = %v, want %v", purposes, want)
	}
	for i := range want {
		if purposes[i] != want[i] {
			t.Fatalf("legacy purposes = %v, want %v", purposes, want)
		}
	}
}
