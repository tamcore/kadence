package store_test

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx database/sql driver
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/tamcore/kadence/internal/store/migrations"
)

func TestChatScheduledHandoffMigrationRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test (needs Docker) in -short mode")
	}
	ctx := context.Background()
	container, err := postgres.Run(ctx, "pgvector/pgvector:pg17",
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
	if err := goose.UpToContext(ctx, db, ".", 17); err != nil {
		t.Fatalf("apply migrations through 17: %v", err)
	}
	assertChatScheduledHandoffSchema(t, ctx, db, false)
	if err := goose.UpToContext(ctx, db, ".", 18); err != nil {
		t.Fatalf("apply migration 18: %v", err)
	}
	assertChatScheduledHandoffSchema(t, ctx, db, true)
	if err := goose.DownToContext(ctx, db, ".", 17); err != nil {
		t.Fatalf("reverse migration 18: %v", err)
	}
	assertChatScheduledHandoffSchema(t, ctx, db, false)
}

func assertChatScheduledHandoffSchema(t *testing.T, ctx context.Context, db *sql.DB, wantPresent bool) {
	t.Helper()
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('chat_scheduled_handoffs') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists != wantPresent {
		t.Fatalf("chat_scheduled_handoffs exists=%t, want %t", exists, wantPresent)
	}
	if !wantPresent {
		return
	}

	var userID, messageID, assistantID int64
	var conversationID, taskID string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (username, email, password_hash, role) VALUES ('handoff-schema', 'handoff-schema@example.com', 'hash', 'user') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO conversations (user_id, title, kind) VALUES ($1, 'source', 'chat') RETURNING id::text`, userID).Scan(&conversationID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO messages (conversation_id, role, content, purpose) VALUES ($1::uuid, 'user', 'source', 'chat') RETURNING id`, conversationID).Scan(&messageID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO messages (conversation_id, role, content, purpose) VALUES ($1::uuid, 'assistant', 'answer', 'chat') RETURNING id`, conversationID).Scan(&assistantID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO chat_scheduled_handoffs (user_id, source_conversation_id, source_user_message_id, source_content_fingerprint, invocation_ordinal, artifact_state)
		 VALUES ($1, $2::uuid, $3, decode(repeat('00', 31), 'hex'), 1, 'creating')`,
		`INSERT INTO chat_scheduled_handoffs (user_id, source_conversation_id, source_user_message_id, source_content_fingerprint, invocation_ordinal, artifact_state)
		 VALUES ($1, $2::uuid, $3, decode(repeat('00', 32), 'hex'), 0, 'creating')`,
		`INSERT INTO chat_scheduled_handoffs (user_id, source_conversation_id, source_user_message_id, source_content_fingerprint, invocation_ordinal, artifact_state)
		 VALUES ($1, $2::uuid, $3, decode(repeat('01', 32), 'hex'), 1, 'invalid')`,
		`INSERT INTO chat_scheduled_handoffs (user_id, source_conversation_id, source_user_message_id, source_content_fingerprint, invocation_ordinal, artifact_state, error_code)
		 VALUES ($1, $2::uuid, $3, decode(repeat('02', 32), 'hex'), 1, 'creating', 'unsafe error')`,
	} {
		if _, err := db.ExecContext(ctx, statement, userID, conversationID, messageID); err == nil {
			t.Fatalf("invalid handoff insert succeeded: %s", statement)
		}
	}
	var scheduledConversationID string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO conversations (user_id, title, kind) VALUES ($1, 'scheduled', 'scheduled') RETURNING id::text`, userID).Scan(&scheduledConversationID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO scheduled_tasks (user_id, conversation_id, kind, state, timezone) VALUES ($1, $2::uuid, 'reminder', 'draft', 'UTC') RETURNING id::text`, userID, scheduledConversationID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	var dismissedTaskID string
	var dismissedConversationID string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO conversations (user_id, title, kind) VALUES ($1, 'dismissed-scheduled', 'scheduled') RETURNING id::text`, userID).Scan(&dismissedConversationID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO scheduled_tasks (user_id, conversation_id, kind, state, timezone) VALUES ($1, $2::uuid, 'reminder', 'draft', 'UTC') RETURNING id::text`, userID, dismissedConversationID).Scan(&dismissedTaskID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO chat_scheduled_handoffs (user_id, source_conversation_id, source_user_message_id, source_content_fingerprint, invocation_ordinal, artifact_state)
		 VALUES ($1, $2::uuid, $3, decode(repeat('05', 32), 'hex'), 1, 'ready')`,
		`INSERT INTO chat_scheduled_handoffs (user_id, source_conversation_id, source_user_message_id, source_content_fingerprint, invocation_ordinal, artifact_state)
		 VALUES ($1, $2::uuid, $3, decode(repeat('06', 32), 'hex'), 1, 'failed')`,
		`INSERT INTO chat_scheduled_handoffs (user_id, source_conversation_id, source_user_message_id, source_content_fingerprint, scheduled_task_id, invocation_ordinal, artifact_state)
		 VALUES ($1, $2::uuid, $3, decode(repeat('07', 32), 'hex'), $4::uuid, 1, 'dismissed')`,
	} {
		if _, err := db.ExecContext(ctx, statement, userID, conversationID, messageID, dismissedTaskID); err == nil {
			t.Fatalf("invalid handoff lifecycle insert succeeded: %s", statement)
		}
	}
	var handoffID string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO chat_scheduled_handoffs (user_id, source_conversation_id, source_user_message_id, source_content_fingerprint, assistant_message_id, scheduled_task_id, invocation_ordinal, artifact_state)
		 VALUES ($1, $2::uuid, $3, decode(repeat('03', 32), 'hex'), $4, $5::uuid, 1, 'ready') RETURNING id::text`, userID, conversationID, messageID, assistantID, taskID).Scan(&handoffID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO chat_scheduled_handoffs (user_id, source_conversation_id, source_user_message_id, source_content_fingerprint, invocation_ordinal, artifact_state)
		 VALUES ($1, $2::uuid, $3, decode(repeat('03', 32), 'hex'), 1, 'creating')`,
		`INSERT INTO chat_scheduled_handoffs (user_id, source_conversation_id, source_user_message_id, source_content_fingerprint, scheduled_task_id, invocation_ordinal, artifact_state)
		 VALUES ($1, $2::uuid, $3, decode(repeat('04', 32), 'hex'), $4::uuid, 1, 'ready')`,
	} {
		if _, err := db.ExecContext(ctx, statement, userID, conversationID, messageID, taskID); err == nil {
			t.Fatalf("duplicate handoff constraint did not reject insert: %s", statement)
		}
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM messages WHERE id = $1`, assistantID); err != nil {
		t.Fatal(err)
	}
	var assistantNull bool
	if err := db.QueryRowContext(ctx, `SELECT assistant_message_id IS NULL FROM chat_scheduled_handoffs WHERE id = $1::uuid`, handoffID).Scan(&assistantNull); err != nil || !assistantNull {
		t.Fatalf("assistant FK did not SET NULL: null=%t err=%v", assistantNull, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE chat_scheduled_handoffs SET artifact_state = 'creating' WHERE id = $1::uuid`, handoffID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM scheduled_tasks WHERE id = $1::uuid`, taskID); err != nil {
		t.Fatal(err)
	}
	var taskNull bool
	if err := db.QueryRowContext(ctx, `SELECT scheduled_task_id IS NULL FROM chat_scheduled_handoffs WHERE id = $1::uuid`, handoffID).Scan(&taskNull); err != nil || !taskNull {
		t.Fatalf("task FK did not SET NULL: null=%t err=%v", taskNull, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM conversations WHERE id = $1::uuid`, conversationID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chat_scheduled_handoffs WHERE id = $1::uuid`, handoffID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("source conversation delete retained handoff count=%d err=%v", count, err)
	}
}
