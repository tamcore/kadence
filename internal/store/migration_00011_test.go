package store_test

import (
	"context"
	"database/sql"
	"math"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pgvector/pgvector-go"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/tamcore/kadence/internal/store/migrations"
)

// TestMigration00011ConvertsWideEmbeddingsAndDropsNarrowOnes exercises the
// *data conversion* semantics of 00011_chunk_hnsw.sql directly against a
// fresh, isolated Postgres container migrated only up to version 10 (the
// schema immediately before 00011). This is deliberately not built on
// testutil.SetupTestDB: that helper's pool is a package-wide singleton
// already migrated to the latest version by the time any test can observe
// it, so a pre-migration insert of an out-of-band-width vector would not be
// possible against it. Skips under -short like the rest of the package's
// Docker-backed tests.
//
// It asserts the true (post-fix) contract:
//   - a >1024-dim row survives the migration, converted to exactly 1024 dims
//     via truncate-to-1024 + L2-renormalize (unit norm).
//   - a <1024-dim row is dropped (this loss is real and permanent — the
//     online re-index worker does not repopulate deleted rows).
func TestMigration00011ConvertsWideEmbeddingsAndDropsNarrowOnes(t *testing.T) {
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

	// Migrate up to (but not including) 00011, so chunks.embedding is still
	// an unconstrained `vector` column that accepts any width.
	const preMigrationVersion = 10
	if err := goose.UpToContext(ctx, db, ".", preMigrationVersion); err != nil {
		t.Fatalf("goose up to %d: %v", preMigrationVersion, err)
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO users (username, email, password_hash, role) VALUES ('u', 'u@example.com', 'h', 'user')`,
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	wide := make([]float32, 1536)
	for i := range wide {
		wide[i] = 1 // constant vector; L2-normalizing truncated-to-1024 prefix is easy to check by hand
	}
	narrow := make([]float32, 512)
	for i := range narrow {
		narrow[i] = 1
	}

	insertChunk := func(content string, v []float32) {
		t.Helper()
		if _, err := db.ExecContext(ctx,
			`INSERT INTO chunks (user_id, scope, source_kind, content, embedding, embedding_model)
			 VALUES ((SELECT id FROM users WHERE username = 'u'), 'private', 'message', $1, $2, 'm1')`,
			content, pgvector.NewVector(v)); err != nil {
			t.Fatalf("insert chunk %q: %v", content, err)
		}
	}
	insertChunk("wide", wide)
	insertChunk("narrow", narrow)

	// Now apply 00011 (and it's the last migration, so this reaches latest).
	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("goose up (apply 00011): %v", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT content, embedding FROM chunks ORDER BY content`)
	if err != nil {
		t.Fatalf("query chunks: %v", err)
	}
	defer func() { _ = rows.Close() }()

	type gotRow struct {
		content string
		vec     pgvector.Vector
	}
	var got []gotRow
	for rows.Next() {
		var r gotRow
		if err := rows.Scan(&r.content, &r.vec); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("expected exactly 1 surviving row (narrow dropped, wide converted), got %d: %+v", len(got), got)
	}
	if got[0].content != "wide" {
		t.Fatalf("surviving row content = %q, want %q (narrow row should have been deleted)", got[0].content, "wide")
	}

	slice := got[0].vec.Slice()
	if len(slice) != 1024 {
		t.Fatalf("surviving row embedding width = %d, want 1024", len(slice))
	}

	var sumSq float64
	for _, f := range slice {
		sumSq += float64(f) * float64(f)
	}
	norm := math.Sqrt(sumSq)
	const tolerance = 1e-4
	if math.Abs(norm-1) > tolerance {
		t.Fatalf("surviving row embedding norm = %v, want ~1 (L2-renormalized)", norm)
	}

	// The source vector was constant (all 1s), so after truncating to the
	// first 1024 dims and L2-renormalizing, every component must be
	// 1/sqrt(1024).
	want := float32(1 / math.Sqrt(1024))
	for i, f := range slice {
		if math.Abs(float64(f-want)) > tolerance {
			t.Fatalf("slice[%d] = %v, want %v (1/sqrt(1024))", i, f, want)
		}
	}
}
