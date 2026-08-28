// Package testdb provisions one isolated PostgreSQL database per test.
//
// The package migrates a template database once per schema version. Every
// call to New clones the template (CREATE DATABASE ... TEMPLATE, a few
// milliseconds) and drops the clone after the test. Each test owns its
// database and can commit freely; t.Parallel() is safe.
//
// The package requires the postgres-test compose service (just test starts
// it): docker compose up postgres-test -d
package testdb

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/database"
	"github.com/kczpl/yet-another-go-boilerplate/migrations"
)

// advisoryLockID makes parallel tests and test binaries create and clone
// the template one at a time. Postgres rejects concurrent operations on a
// database that is in use as a clone source.
//
// If the suite grows and this lock becomes the bottleneck, narrow it to
// the template creation and let the clones run in parallel. Measure
// first: some PostgreSQL versions serialize clones of one template on
// their own.
const advisoryLockID = 987_654_321

// New returns a pool that is connected to a fresh, fully migrated database.
// Only this test owns the database.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	base := baseURL()

	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connecting to test postgres: %v\n\nstart it with: docker compose up postgres-test -d", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockID); err != nil {
		t.Fatalf("acquiring advisory lock: %v", err)
	}
	defer func() {
		_, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, advisoryLockID)
	}()

	template, err := ensureTemplate(ctx, conn, base)
	if err != nil {
		t.Fatalf("preparing template database: %v", err)
	}

	name := "test_" + randomSuffix()
	if _, err := conn.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q TEMPLATE %q`, name, template)); err != nil {
		t.Fatalf("creating test database: %v", err)
	}

	pool, err := pgxpool.New(ctx, withDatabase(base, name))
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		dropCtx := context.Background()
		conn, err := pgx.Connect(dropCtx, base)
		if err != nil {
			t.Logf("dropping test database %s: %v", name, err)
			return
		}
		defer conn.Close(dropCtx)
		if _, err := conn.Exec(dropCtx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, name)); err != nil {
			t.Logf("dropping test database %s: %v", name, err)
		}
	})
	return pool
}

// ensureTemplate creates and migrates the template database if this schema
// version does not have one yet. Hold the advisory lock when you call it.
// It also drops templates from older schema versions.
func ensureTemplate(ctx context.Context, conn *pgx.Conn, base string) (string, error) {
	name := "app_test_template_" + migrations.Hash()

	var exists bool
	if err := conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name,
	).Scan(&exists); err != nil {
		return "", fmt.Errorf("checking for template: %w", err)
	}
	if exists {
		return name, nil
	}

	if err := dropStaleTemplates(ctx, conn, name); err != nil {
		return "", err
	}

	if _, err := conn.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, name)); err != nil {
		return "", fmt.Errorf("creating template: %w", err)
	}

	pool, err := pgxpool.New(ctx, withDatabase(base, name))
	if err != nil {
		return "", fmt.Errorf("connecting to template: %w", err)
	}
	err = database.Migrate(ctx, pool, migrations.FS)
	// A clone requires zero connections to the template, so close the pool
	// first.
	pool.Close()
	if err != nil {
		// Do not leave a half-migrated template behind for the next run.
		_, _ = conn.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q`, name))
		return "", fmt.Errorf("migrating template: %w", err)
	}
	return name, nil
}

func dropStaleTemplates(ctx context.Context, conn *pgx.Conn, current string) error {
	rows, err := conn.Query(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE 'app_test_template_%' AND datname <> $1`, current)
	if err != nil {
		return fmt.Errorf("listing stale templates: %w", err)
	}
	stale, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("reading stale templates: %w", err)
	}
	for _, name := range stale {
		if _, err := conn.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, name)); err != nil {
			return fmt.Errorf("dropping stale template %s: %w", name, err)
		}
	}
	return nil
}

// baseURL returns the maintenance database URL of the test server.
// TEST_DATABASE_URL overrides everything. POSTGRES_TEST_PORT matches the
// docker-compose port mapping.
func baseURL() string {
	if u := os.Getenv("TEST_DATABASE_URL"); u != "" {
		return u
	}
	port := cmp.Or(os.Getenv("POSTGRES_TEST_PORT"), "5433")
	return "postgres://app_test:app_test@localhost:" + port + "/app_test"
}

// withDatabase swaps the database name in a connection URL. An earlier
// connection already used base, so base parses.
func withDatabase(base, dbname string) string {
	u, err := url.Parse(base)
	if err != nil {
		panic(fmt.Sprintf("parsing database url: %v", err))
	}
	u.Path = "/" + dbname
	return u.String()
}

func randomSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return hex.EncodeToString(b)
}
