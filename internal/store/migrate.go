package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"

	"github.com/tamcore/kadence/internal/store/migrations"
)

// migrateLockKey is an arbitrary but fixed advisory-lock key so that
// concurrent instances serialize schema migrations.
const migrateLockKey int64 = 4927015

// Migrate runs all pending goose "up" migrations, serialized across instances
// via a Postgres session-level advisory lock. Safe to call on every startup.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	db, err := sql.Open("pgx", pool.Config().ConnString())
	if err != nil {
		return fmt.Errorf("open sql db for migrate: %w", err)
	}
	defer func() {
		_ = db.Close()
	}()

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migrate conn: %w", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrateLockKey); err != nil {
		return fmt.Errorf("acquire advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", migrateLockKey)
	}()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
