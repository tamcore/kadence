package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/store/testutil"
)

func TestWaitForDB(t *testing.T) {
	t.Run("returns nil once a ping succeeds", func(t *testing.T) {
		pool := testutil.SetupTestDB(t)
		dsn := pool.Config().ConnString()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := store.WaitForDB(ctx, dsn); err != nil {
			t.Fatalf("WaitForDB() = %v, want nil", err)
		}
	})

	t.Run("returns ctx error when deadline passes with unreachable DB", func(t *testing.T) {
		// Unreachable DSN: port 1 is not a listening Postgres, so pings will
		// keep failing until the context deadline is exceeded.
		const badDSN = "postgres://user:pass@127.0.0.1:1/nosuchdb?sslmode=disable"

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		err := store.WaitForDB(ctx, badDSN)
		if err == nil {
			t.Fatal("WaitForDB() = nil, want error for unreachable DB")
		}
	})
}
