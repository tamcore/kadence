// Package store provides Postgres access: connection pooling, migrations, and repositories.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvector "github.com/pgvector/pgvector-go/pgx"
)

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

// Close closes the pool if non-nil.
func Close(pool *pgxpool.Pool) {
	if pool != nil {
		pool.Close()
	}
}
