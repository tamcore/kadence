package serve

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/store"
)

// waitForDBTimeout bounds how long the wait-for-db entrypoint will retry
// before giving up and exiting non-zero (surfaced as initContainer failure).
const waitForDBTimeout = 120 * time.Second

// WaitForDB is the entrypoint for the "wait-for-db" subcommand. It blocks
// until the configured database is reachable or waitForDBTimeout elapses,
// returning an error in the latter case. Intended to run as a Kubernetes
// initContainer ahead of the main server container.
func WaitForDB() error {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), waitForDBTimeout)
	defer cancel()

	slog.Info("waiting for database", "timeout", waitForDBTimeout)
	if err := store.WaitForDB(ctx, cfg.DatabaseURL); err != nil {
		return fmt.Errorf("wait for database: %w", err)
	}

	slog.Info("database ready")
	return nil
}
