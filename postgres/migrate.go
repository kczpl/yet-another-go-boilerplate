package postgres

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
)

// The embedded migrations are the single source of schema truth: applied on
// app startup and when testdb builds its template database.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies all pending migrations. It is idempotent and safe to run on
// every startup.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	fsys, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("reading embedded migrations: %w", err)
	}

	// goose speaks database/sql; borrow the pool through the stdlib adapter.
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	provider, err := goose.NewProvider(database.DialectPostgres, db, fsys)
	if err != nil {
		return fmt.Errorf("creating goose provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}
	return nil
}

// MigrationsHash fingerprints the embedded migration files. testdb bakes it
// into template database names so schema changes invalidate old templates.
func MigrationsHash() string {
	h := sha256.New()
	_ = fs.WalkDir(migrationsFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, err := migrationsFS.ReadFile(path)
		if err != nil {
			return err
		}
		h.Write([]byte(path))
		h.Write(content)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:12]
}
