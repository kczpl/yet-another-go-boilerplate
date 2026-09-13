package database_test

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/database"
	"github.com/kczpl/yet-another-go-boilerplate/internal/testdb"
)

func TestMigrateConcurrentAndLateFiles(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	files := fstest.MapFS{"9002_create_probe.sql": {Data: []byte("CREATE TABLE migration_probe (id integer PRIMARY KEY)")}}
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- database.Migrate(t.Context(), pool, files) }()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	files["9001_late.sql"] = &fstest.MapFile{Data: []byte("INSERT INTO migration_probe VALUES (1)")}
	for range 2 {
		if err := database.Migrate(t.Context(), pool, files); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM migration_probe").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("late migration inserted %d rows, want 1", count)
	}
}

func TestMigrateRollsBackFailedFile(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	files := fstest.MapFS{"9001_failure.sql": {Data: []byte("CREATE TABLE rollback_probe (id integer); SELECT 1 / 0;")}}
	if err := database.Migrate(t.Context(), pool, files); err == nil {
		t.Fatal("failed migration succeeded")
	}
	var absent bool
	if err := pool.QueryRow(t.Context(), "SELECT to_regclass('rollback_probe') IS NULL").Scan(&absent); err != nil {
		t.Fatal(err)
	}
	if !absent {
		t.Fatal("failed migration left its table behind")
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM schema_migrations WHERE filename = '9001_failure.sql'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed migration was recorded as applied")
	}
	files["9001_failure.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE rollback_probe (id integer)")}
	if err := database.Migrate(t.Context(), pool, files); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestMigrateCancellationReleasesLock(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx, cancel := context.WithCancel(t.Context())
	files := fstest.MapFS{"9001_cancel.sql": {Data: []byte("SELECT 1")}}
	fsys := cancelFS{MapFS: files, cancel: cancel}
	if err := database.Migrate(ctx, pool, fsys); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if err := database.Migrate(t.Context(), pool, files); err != nil {
		t.Fatal(err)
	}
	var locks int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`).Scan(&locks); err != nil {
		t.Fatal(err)
	}
	if locks != 0 {
		t.Fatalf("migration retained %d advisory locks", locks)
	}
}

type cancelFS struct {
	fstest.MapFS
	cancel context.CancelFunc
}

func (f cancelFS) ReadFile(name string) ([]byte, error) {
	f.cancel()
	return f.MapFS.ReadFile(name)
}
