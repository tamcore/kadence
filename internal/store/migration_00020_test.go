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

func TestConversationNavigationMigrationBackfillsAndReverses(t *testing.T) {
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
	if err := goose.UpToContext(ctx, db, ".", 19); err != nil {
		t.Fatalf("apply migrations through 19: %v", err)
	}

	var userID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (username, email, password_hash, role) VALUES ('navigation-migration', 'navigation-migration@example.com', 'hash', 'user') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)
	messageAt := createdAt.Add(3 * time.Hour)
	var withMessage, withoutMessage string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO conversations (user_id, title, kind, created_at) VALUES ($1, 'with message', 'chat', $2) RETURNING id::text`, userID, createdAt).Scan(&withMessage); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO conversations (user_id, title, kind, created_at) VALUES ($1, 'without message', 'chat', $2) RETURNING id::text`, userID, createdAt).Scan(&withoutMessage); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO messages (conversation_id, role, content, purpose, created_at) VALUES ($1::uuid, 'user', 'newest', 'chat', $2)`, withMessage, messageAt); err != nil {
		t.Fatal(err)
	}

	if err := goose.UpToContext(ctx, db, ".", 20); err != nil {
		t.Fatalf("apply migration 20: %v", err)
	}
	for _, tc := range []struct {
		id   string
		want time.Time
	}{
		{id: withMessage, want: messageAt},
		{id: withoutMessage, want: createdAt},
	} {
		var pinnedAt *time.Time
		var lastActivityAt time.Time
		if err := db.QueryRowContext(ctx,
			`SELECT pinned_at, last_activity_at FROM conversations WHERE id = $1::uuid`, tc.id).Scan(&pinnedAt, &lastActivityAt); err != nil {
			t.Fatal(err)
		}
		if pinnedAt != nil || !lastActivityAt.Equal(tc.want) {
			t.Fatalf("conversation %s pinned=%v last_activity_at=%s want=%s", tc.id, pinnedAt, lastActivityAt, tc.want)
		}
	}
	if err := goose.DownToContext(ctx, db, ".", 19); err != nil {
		t.Fatalf("reverse migration 20: %v", err)
	}
	var navigationColumns int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM information_schema.columns WHERE table_name = 'conversations' AND column_name IN ('pinned_at', 'last_activity_at')`).Scan(&navigationColumns); err != nil {
		t.Fatal(err)
	}
	if navigationColumns != 0 {
		t.Fatalf("navigation columns remain after down migration: %d", navigationColumns)
	}
}
