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

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store/migrations"
)

// TestMigration00021DeliveryConversation exercises 00021's schema addition and
// backfill: a direct schedule (no handoff) should deliver into its own
// conversation (which is promoted from kind='scheduled' to kind='chat'), and
// a chat-originated schedule (source chat + scheduled conversation + handoff)
// should deliver into the source chat, with its past delivery message moved
// there.
func TestMigration00021DeliveryConversation(t *testing.T) {
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

	// Migrate up to (but not including) 00021 so the fixture can be built
	// against the pre-migration schema.
	const preMigrationVersion = 20
	if err := goose.UpToContext(ctx, db, ".", preMigrationVersion); err != nil {
		t.Fatalf("apply migrations through %d: %v", preMigrationVersion, err)
	}

	// Arrange: a direct schedule (scheduled conversation, no handoff) with one
	// past delivery message, and a chat-originated schedule (source chat +
	// scheduled conversation + handoff) with one past delivery message.
	var userID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (username, email, password_hash, role)
		 VALUES ('sched_deliv', 'sched_deliv@example.com', 'x', 'user') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	// Direct schedule.
	var directConv string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO conversations (user_id, title, kind) VALUES ($1, 'direct', 'scheduled') RETURNING id::text`,
		userID).Scan(&directConv); err != nil {
		t.Fatalf("insert direct conversation: %v", err)
	}
	var directTask string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO scheduled_tasks (user_id, conversation_id, name, kind, state, timezone, compiled_prompt)
		 VALUES ($1, $2::uuid, 'direct', 'reminder', 'active', 'UTC', 'p') RETURNING id::text`,
		userID, directConv).Scan(&directTask); err != nil {
		t.Fatalf("insert direct task: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO messages (conversation_id, role, content, purpose)
		 VALUES ($1::uuid, 'assistant', 'direct result', 'scheduled_delivery')`, directConv); err != nil {
		t.Fatalf("insert direct delivery: %v", err)
	}

	// Chat-originated schedule.
	var sourceChat string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO conversations (user_id, title, kind) VALUES ($1, 'source', 'chat') RETURNING id::text`,
		userID).Scan(&sourceChat); err != nil {
		t.Fatalf("insert source chat: %v", err)
	}
	var srcMsgID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO messages (conversation_id, role, content, purpose)
		 VALUES ($1::uuid, 'user', 'please schedule', 'chat') RETURNING id`, sourceChat).Scan(&srcMsgID); err != nil {
		t.Fatalf("insert source user message: %v", err)
	}
	var handoffConv string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO conversations (user_id, title, kind) VALUES ($1, 'handoff', 'scheduled') RETURNING id::text`,
		userID).Scan(&handoffConv); err != nil {
		t.Fatalf("insert handoff conversation: %v", err)
	}
	var handoffTask string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO scheduled_tasks (user_id, conversation_id, name, kind, state, timezone, compiled_prompt)
		 VALUES ($1, $2::uuid, 'handoff', 'reminder', 'active', 'UTC', 'p') RETURNING id::text`,
		userID, handoffConv).Scan(&handoffTask); err != nil {
		t.Fatalf("insert handoff task: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO chat_scheduled_handoffs
		   (user_id, source_conversation_id, source_user_message_id, source_content_fingerprint,
		    scheduled_task_id, invocation_ordinal, artifact_state)
		 VALUES ($1, $2::uuid, $3, decode(repeat('ab',32),'hex'), $4::uuid, 1, 'ready')`,
		userID, sourceChat, srcMsgID, handoffTask); err != nil {
		t.Fatalf("insert handoff: %v", err)
	}
	var handoffDeliveryID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO messages (conversation_id, role, content, purpose)
		 VALUES ($1::uuid, 'assistant', 'handoff result', 'scheduled_delivery') RETURNING id`,
		handoffConv).Scan(&handoffDeliveryID); err != nil {
		t.Fatalf("insert handoff delivery: %v", err)
	}

	// Act: run migration 00021.
	if err := goose.UpToContext(ctx, db, ".", 21); err != nil {
		t.Fatalf("apply migration 21: %v", err)
	}

	// Assert: direct task delivers into its own conversation, now kind='chat'.
	var directDelivery, directKind string
	if err := db.QueryRowContext(ctx,
		`SELECT t.delivery_conversation_id::text, c.kind
		   FROM scheduled_tasks t JOIN conversations c ON c.id = t.conversation_id
		  WHERE t.id = $1::uuid`, directTask).Scan(&directDelivery, &directKind); err != nil {
		t.Fatalf("read direct task: %v", err)
	}
	if directDelivery != directConv {
		t.Fatalf("direct delivery = %s, want %s", directDelivery, directConv)
	}
	if directKind != model.ConversationKindChat {
		t.Fatalf("direct conversation kind = %s, want chat", directKind)
	}

	// Assert: chat-originated task delivers into the source chat, and its past
	// delivery message was moved there.
	var handoffDelivery string
	if err := db.QueryRowContext(ctx,
		`SELECT delivery_conversation_id::text FROM scheduled_tasks WHERE id = $1::uuid`, handoffTask).Scan(&handoffDelivery); err != nil {
		t.Fatalf("read handoff task: %v", err)
	}
	if handoffDelivery != sourceChat {
		t.Fatalf("handoff delivery = %s, want %s", handoffDelivery, sourceChat)
	}
	var movedConv string
	if err := db.QueryRowContext(ctx,
		`SELECT conversation_id::text FROM messages WHERE id = $1`, handoffDeliveryID).Scan(&movedConv); err != nil {
		t.Fatalf("read moved delivery: %v", err)
	}
	if movedConv != sourceChat {
		t.Fatalf("moved delivery conversation = %s, want %s", movedConv, sourceChat)
	}
}
