package database

import (
	"context"
	"fmt"
	"io/fs"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrateLockID serializes concurrent migrators via an advisory lock. The
// value is arbitrary but must be unique on the server.
const migrateLockID = 776_012_345

// Migrate applies each new *.sql file from fsys in lexical order, one
// transaction per file. It records applied filenames in schema_migrations.
// Migrations are append-only: never edit an applied file.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) error {
	// Advisory locks are session-scoped: lock and unlock must use the same
	// connection.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquiring connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrateLockID); err != nil {
		return fmt.Errorf("acquiring migration lock: %w", err)
	}
	defer conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrateLockID)

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename   text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("creating schema_migrations table: %w", err)
	}

	files, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return fmt.Errorf("listing migrations: %w", err)
	}
	slices.Sort(files)

	rows, err := conn.Query(ctx, "SELECT filename FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("listing applied migrations: %w", err)
	}
	applied, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("reading applied migrations: %w", err)
	}

	for _, file := range files {
		if slices.Contains(applied, file) {
			continue
		}
		sql, err := fs.ReadFile(fsys, file)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", file, err)
		}
		err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			// Exec without arguments uses the simple protocol, so one file
			// can hold multiple statements.
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "INSERT INTO schema_migrations (filename) VALUES ($1)", file)
			return err
		})
		if err != nil {
			return fmt.Errorf("applying migration %s: %w", file, err)
		}
	}
	return nil
}
