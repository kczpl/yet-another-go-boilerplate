package database_test

import (
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/config"
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
			logger := logging.New(&out, config.Config{LogLevel: level})
			pool, err := database.Connect(t.Context(), base.Config().ConnString(), logger)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pool.Close)
			ctx := logging.WithAttrs(t.Context(), slog.String("request_id", "trace-123"))
			_, err = pool.Exec(ctx, `INSERT INTO users (email, name, password_hash) VALUES (@email, @name, @hash)`, pgx.NamedArgs{
				"email": level.String() + "@private.example", "name": "Private Name", "hash": "pbkdf2-sha256$private-hash",
			})
			if err != nil {
				t.Fatal(err)
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
