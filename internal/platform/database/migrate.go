package database

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"time"

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

	// Register the release first. If the lock call fails after the server
	// granted the lock, the release still removes it.
	defer releaseMigrationLock(conn)
	if err := acquireMigrationLock(ctx, conn); err != nil {
		return fmt.Errorf("acquiring migration lock: %w", err)
	}

	files, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return fmt.Errorf("listing migrations: %w", err)
	}
	slices.Sort(files)

	applied, err := appliedMigrations(ctx, conn)
	if err != nil {
		return err
	}

	for _, file := range files {
		if slices.Contains(applied, file) {
			continue
		}
		if err := applyMigration(ctx, conn, fsys, file); err != nil {
			return fmt.Errorf("applying migration %s: %w", file, err)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, conn *pgxpool.Conn, fsys fs.FS, file string) error {
	sql, err := fs.ReadFile(fsys, file)
	if err != nil {
		return fmt.Errorf("reading migration: %w", err)
	}
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		// A migration can run longer than the pool's statement timeout,
		// for example when it builds an index. SET LOCAL ends with the
		// transaction.
		if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = 0"); err != nil {
			return err
		}
		// Exec without arguments uses the simple protocol.
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "INSERT INTO schema_migrations (filename) VALUES ($1)", file)
		return err
	})
}

// acquireMigrationLock waits for the session-scoped advisory lock. Another
// migrator can hold the lock for the whole length of its migrations, so the
// wait has no statement timeout. SET LOCAL ends with the transaction. The
// session lock stays after the commit.
func acquireMigrationLock(ctx context.Context, conn *pgxpool.Conn) error {
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = 0"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "SELECT pg_advisory_lock($1)", migrateLockID)
		return err
	})
}

// releaseMigrationLock removes the advisory lock. If the unlock fails, or
// the session does not hold the lock, it closes the connection: the server
// then drops every lock of the session.
func releaseMigrationLock(conn *pgxpool.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var unlocked bool
	err := conn.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", migrateLockID).Scan(&unlocked)
	if err != nil || !unlocked {
		// Close the connection so the pool cannot retain a session lock.
		_ = conn.Conn().Close(ctx)
	}
}

func appliedMigrations(ctx context.Context, conn *pgxpool.Conn) ([]string, error) {
	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename   text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return nil, fmt.Errorf("creating schema_migrations table: %w", err)
	}

	rows, err := conn.Query(ctx, "SELECT filename FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("listing applied migrations: %w", err)
	}
	applied, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("reading applied migrations: %w", err)
	}

	return applied, nil
}
