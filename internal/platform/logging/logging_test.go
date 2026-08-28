package logging_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/config"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/logging"
)

// jsonLogger returns a logger that writes JSON lines into buf.
func jsonLogger(buf *bytes.Buffer) *slog.Logger {
	return logging.New(buf, config.Config{Env: "production"})
}

func lastLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("parsing log line %q: %v", buf.String(), err)
	}
	return line
}

func TestContextAttrsReachRecords(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := jsonLogger(&buf)

	ctx := logging.WithAttrs(t.Context(), slog.String("request_id", "req-1"))
	ctx = logging.WithAttrs(ctx, slog.String("user_id", "user-1"))
	logger.InfoContext(ctx, "hello", "extra", "value")

	line := lastLine(t, &buf)
	if line["request_id"] != "req-1" {
		t.Errorf("request_id = %v, want req-1", line["request_id"])
	}
	if line["user_id"] != "user-1" {
		t.Errorf("user_id = %v, want user-1", line["user_id"])
	}
	if line["extra"] != "value" {
		t.Errorf("extra = %v, want value", line["extra"])
	}
}

func TestWithAttrsDoesNotMutateParent(t *testing.T) {
	t.Parallel()
	parent := logging.WithAttrs(t.Context(), slog.String("request_id", "req-1"))
	// Two children derive from the same parent. Neither child may leak
	// into the parent.
	_ = logging.WithAttrs(parent, slog.String("user_id", "a"))
	_ = logging.WithAttrs(parent, slog.String("user_id", "b"))

	var buf bytes.Buffer
	jsonLogger(&buf).InfoContext(parent, "hello")

	line := lastLine(t, &buf)
	if line["request_id"] != "req-1" {
		t.Errorf("request_id = %v, want req-1", line["request_id"])
	}
	if _, ok := line["user_id"]; ok {
		t.Errorf("parent context leaked a child attr: %v", line)
	}
}

func TestPlainLoggingStillWorks(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	jsonLogger(&buf).Info("startup", "addr", ":8080")

	line := lastLine(t, &buf)
	if line["addr"] != ":8080" {
		t.Errorf("addr = %v, want :8080", line["addr"])
	}
}
