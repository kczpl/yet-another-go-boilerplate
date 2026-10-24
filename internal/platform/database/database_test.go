package database_test

import (
	"io"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/database"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/logging"
	"github.com/kczpl/yet-another-go-boilerplate/internal/testdb"
)

type logBuffer struct {
	mu   sync.Mutex
	text strings.Builder
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.String()
}

func TestQueryLogsOmitPrivateData(t *testing.T) {
	t.Parallel()
	base := testdb.New(t)
	for _, level := range []slog.Level{slog.LevelInfo, slog.LevelDebug} {
		t.Run(level.String(), func(t *testing.T) {
			var out logBuffer
			logger := logging.New(&out, level, logging.JSON)
			pool, err := database.Connect(t.Context(), base.Config().ConnString(), logger)
			if err != nil {
				t.Fatalf("Connect: %v", err)
			}
			t.Cleanup(pool.Close)
			ctx := logging.WithAttrs(t.Context(), slog.String("request_id", "trace-123"))
			_, err = pool.Exec(ctx, `INSERT INTO users (email, name, password_hash) VALUES (@email, @name, @hash)`, pgx.NamedArgs{
				"email": level.String() + "@private.example", "name": "Private Name", "hash": "pbkdf2-sha256$private-hash",
			})
			if err != nil {
				t.Fatalf("inserting a user: %v", err)
			}
			_, err = pool.Exec(ctx, "SELECT $1::integer", "private-invalid-input")
			if err == nil {
				t.Fatal("invalid query succeeded")
			}
			output := out.String()
			for _, secret := range []string{"@private.example", "Private Name", "pbkdf2-sha256$private-hash", "private-invalid-input", `"args"`} {
				if strings.Contains(output, secret) {
					t.Errorf("log contains private value %q", secret)
				}
			}
			if strings.Contains(output, "pgx:") != (level == slog.LevelDebug) {
				t.Errorf("unexpected trace at %s: %s", level, output)
			}
			if level == slog.LevelDebug && !strings.Contains(output, "trace-123") {
				t.Error("query lost its request id")
			}
		})
	}
}

// withParam returns connString with one more URL query parameter.
func withParam(t *testing.T, connString, key, value string) string {
	t.Helper()
	u, err := url.Parse(connString)
	if err != nil {
		t.Fatalf("parsing connection string: %v", err)
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String()
}

func TestConnectSetsStatementTimeout(t *testing.T) {
	t.Parallel()
	base := testdb.New(t).Config().ConnString()
	logger := logging.New(io.Discard, slog.LevelInfo, logging.JSON)

	tests := []struct {
		name string
		url  string
		want string
	}{
		{"default", base, "5s"},
		{"URL overrides the default", withParam(t, base, "statement_timeout", "30s"), "30s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := database.Connect(t.Context(), tt.url, logger)
			if err != nil {
				t.Fatalf("Connect: %v", err)
			}
			t.Cleanup(pool.Close)
			var got string
			if err := pool.QueryRow(t.Context(), "SHOW statement_timeout").Scan(&got); err != nil {
				t.Fatalf("SHOW statement_timeout: %v", err)
			}
			if got != tt.want {
				t.Errorf("statement_timeout = %q, want %q", got, tt.want)
			}
		})
	}
}
