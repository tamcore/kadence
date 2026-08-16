// Package testutil provides a Postgres testcontainer for integration tests.
// All helpers skip under `go test -short`.
package testutil

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/tamcore/kadence/internal/store"
)

// PostgresImage is the pgvector-enabled Postgres image every DB-backed test
// runs against. Pinned to the same release the chart deploys
// (charts/kadence/values.yaml: postgres.image) so a pg-minor-specific migration
// problem cannot pass the tests and still break a real install. Bump together.
const PostgresImage = "pgvector/pgvector:0.8.6-pg17-bookworm"

var (
	once sync.Once
	pool *pgxpool.Pool
)

// SetupTestDB starts (once per package) a Postgres container, runs migrations,
// and returns a shared pool truncated to an empty state. Skips the calling test
// under -short.
//
// The container and pool are shared by every test in the package, so isolation
// comes from the truncate on each call rather than from a fresh database. Tests
// using this pool must therefore not run in parallel with each other.
func SetupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test (needs Docker) in -short mode")
	}
	once.Do(func() {
		ctx := context.Background()
		container, err := postgres.Run(ctx, PostgresImage,
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
		dsn, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			t.Fatalf("connection string: %v", err)
		}
		p, err := store.Open(ctx, dsn)
		if err != nil {
			t.Fatalf("open pool: %v", err)
		}
		if err := store.Migrate(ctx, p); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		pool = p
	})
	if pool == nil {
		t.Fatal("test pool not initialized")
	}
	CleanTables(t, pool)
	return pool
}

// CleanTables truncates all data tables. SetupTestDB already calls it, so this
// is only for a test that needs to reset again part-way through — e.g. a
// subtest loop that reuses one package-level pool.
func CleanTables(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	_, err := p.Exec(context.Background(), "TRUNCATE mcp_call_audit, users, sessions, documents RESTART IDENTITY CASCADE")
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}
