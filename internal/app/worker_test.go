package app_test

import (
	"io"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kczpl/yet-another-go-boilerplate/internal/app"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/config"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/logging"
	"github.com/kczpl/yet-another-go-boilerplate/internal/testdb"
)

// TestWorkerDeletesExpiredSessions runs the worker as cmd/api wires it.
// The hourly schedule sends the session cleanup to the auth queue, the
// consumer of the migration handles it, and the cleanup keeps the live
// session.
func TestWorkerDeletesExpiredSessions(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	_, err := pool.Exec(t.Context(), `
		WITH u AS (
			INSERT INTO users (email, name, password_hash)
			VALUES ('worker@example.com', 'Worker Test', 'x')
			RETURNING id
		)
		INSERT INTO sessions (token_hash, user_id, expires_at)
		SELECT 'expired', id, now() - interval '1 hour' FROM u
		UNION ALL
		SELECT 'live', id, now() + interval '1 hour' FROM u`)
	if err != nil {
		t.Fatalf("seeding sessions: %v", err)
	}
	cfg := config.Load(func(string) string { return "" })
	w := app.NewWorker(logging.New(io.Discard, cfg.LogLevel, logging.JSON), cfg, pool)

	handled, err := w.RunDue(t.Context())
	if err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	if handled != 1 {
		t.Fatalf("RunDue handled %d messages, want the one cleanup", handled)
	}
	var dead int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM pgque.dead_letter").Scan(&dead); err != nil || dead != 0 {
		t.Fatalf("dead letters = %d, %v; want 0", dead, err)
	}
	rows, _ := pool.Query(t.Context(), "SELECT token_hash FROM sessions")
	left, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("reading sessions: %v", err)
	}
	if len(left) != 1 || left[0] != "live" {
		t.Errorf("sessions left = %v, want [live]", left)
	}
	// The slot of this hour is spent.
	if handled, err := w.RunDue(t.Context()); err != nil || handled != 0 {
		t.Errorf("second RunDue handled %d messages, %v; want 0", handled, err)
	}
}
