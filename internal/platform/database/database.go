// Package database provides the PostgreSQL connection pool and the schema
// migrator. It knows nothing about the application's tables.
package database

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/tracelog"
)

// defaultStatementTimeout stops a slow query before it holds a pool
// connection for the whole request. The server cancels the statement, and
// the caller gets an error.
const defaultStatementTimeout = "5s"

// Connect opens a pgx pool and pings it. A service that cannot reach its
// database must not start. Queries are traced into logger at debug level.
//
// Each connection gets statement_timeout = defaultStatementTimeout. To
// change it, add statement_timeout to the URL (for example
// ?statement_timeout=30s). The value 0 disables the limit.
func Connect(ctx context.Context, url string, logger *slog.Logger) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parsing database url: %w", err)
	}
	if _, ok := cfg.ConnConfig.RuntimeParams["statement_timeout"]; !ok {
		cfg.ConnConfig.RuntimeParams["statement_timeout"] = defaultStatementTimeout
	}
	if logger.Enabled(ctx, slog.LevelDebug) {
		cfg.ConnConfig.Tracer = &tracelog.TraceLog{
			Logger:   queryLogger(logger),
			LogLevel: tracelog.LogLevelTrace,
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return pool, nil
}

// queryLogger adapts pgx tracelog to slog. The request context passes
// through, so query logs carry request_id and user_id.
func queryLogger(logger *slog.Logger) tracelog.LoggerFunc {
	return func(ctx context.Context, _ tracelog.LogLevel, msg string, data map[string]any) {
		attrs := make([]slog.Attr, 0, len(data))
		for k, v := range data {
			// Arguments and driver errors can contain private data.
			if k == "args" || k == "err" {
				continue
			}
			attrs = append(attrs, slog.Any(k, v))
		}
		logger.LogAttrs(ctx, slog.LevelDebug, "pgx: "+msg, attrs...)
	}
}
