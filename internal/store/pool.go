// Package store provides Postgres access: connection pooling, migrations, and repositories.
package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvector "github.com/pgvector/pgvector-go/pgx"
)

// waitForDBRetryInterval is how long WaitForDB sleeps between failed
// connect+ping attempts while waiting for Postgres to become reachable.
const waitForDBRetryInterval = 2 * time.Second

// Open creates a pgx connection pool from a DSN and verifies connectivity.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MaxConnLifetime = time.Hour
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_ = pgxvector.RegisterTypes(ctx, conn) // best-effort; vector type exists after migration 00003
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// WaitForDB blocks until a connection to dsn can be opened and pinged, or
// until ctx is done. It reuses Open for the actual connect+ping logic (DRY)
// so the pool built here is closed immediately on success — WaitForDB only
// reports readiness, it does not hand back a usable pool.
func WaitForDB(ctx context.Context, dsn string) error {
	for {
		pool, err := Open(ctx, dsn)
		if err == nil {
			pool.Close()
			return nil
		}

		slog.Warn("database not ready, retrying", "err", err, "retry_in", waitForDBRetryInterval)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitForDBRetryInterval):
		}
	}
}

// Close closes the pool if non-nil.
func Close(pool *pgxpool.Pool) {
	if pool != nil {
		pool.Close()
	}
}

// rowScanner is the part of pgx.Row and pgx.Rows the repositories' scan
// helpers need, so one helper can serve a QueryRow result and a rows cursor.
type rowScanner interface{ Scan(...any) error }

// inTx runs fn inside a transaction: it rolls back when fn fails and commits
// otherwise, so no caller can forget either. wrap names the operation in the
// begin/commit error messages ("begin <wrap>", "commit <wrap>"); errors from
// fn itself pass through unwrapped, since they already describe themselves.
func inTx[T any](ctx context.Context, pool *pgxpool.Pool, wrap string, fn func(pgx.Tx) (T, error)) (T, error) {
	var zero T
	tx, err := pool.Begin(ctx)
	if err != nil {
		return zero, fmt.Errorf("begin %s: %w", wrap, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out, err := fn(tx)
	if err != nil {
		return zero, err
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, fmt.Errorf("commit %s: %w", wrap, err)
	}
	return out, nil
}

// inTxErr is inTx for operations that produce no value beyond success.
func inTxErr(ctx context.Context, pool *pgxpool.Pool, wrap string, fn func(pgx.Tx) error) error {
	_, err := inTx(ctx, pool, wrap, func(tx pgx.Tx) (struct{}, error) {
		return struct{}{}, fn(tx)
	})
	return err
}
