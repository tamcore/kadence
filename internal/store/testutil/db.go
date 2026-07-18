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

const pgImage = "pgvector/pgvector:pg17" // pgvector-enabled; reused by later RAG phase

var (
	once sync.Once
	pool *pgxpool.Pool
)

// SetupTestDB starts (once per package) a Postgres container, runs migrations,
// and returns a shared pool. Skips the calling test under -short.
func SetupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test (needs Docker) in -short mode")
	}
	once.Do(func() {
		ctx := context.Background()
		container, err := postgres.Run(ctx, pgImage,
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
	return pool
}

// CleanTables truncates all data tables for test isolation.
func CleanTables(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	_, err := p.Exec(context.Background(), "TRUNCATE users, sessions RESTART IDENTITY CASCADE")
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}
