// The api binary. main stays trivial; run owns startup and takes its
// environment (args, env, output) as arguments so tests can call it like any
// other function.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kczpl/yet-another-go-boilerplate/api"
	"github.com/kczpl/yet-another-go-boilerplate/config"
	"github.com/kczpl/yet-another-go-boilerplate/domains/notes"
	"github.com/kczpl/yet-another-go-boilerplate/postgres"
)

func main() {
	ctx := context.Background()
	if err := run(ctx, os.Args, os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.Load(getenv)
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	logger := newLogger(stdout, cfg)

	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to postgres: %w", err)
	}
	defer pool.Close()

	if err := postgres.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	// `go run . migrate` applies migrations and exits — handy for CI and ops.
	if len(args) > 1 && args[1] == "migrate" {
		logger.Info("migrations applied")
		return nil
	}

	notesService := notes.NewService(notes.NewRepo(pool))

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           api.NewServer(logger, cfg, pool, notesService),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	logger.Info("server started", "addr", srv.Addr, "env", cfg.Env)

	select {
	case err := <-errCh:
		return fmt.Errorf("listening and serving: %w", err)
	case <-ctx.Done():
		// The signal context is already canceled; shutdown needs its own deadline.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutting down: %w", err)
		}
		logger.Info("server stopped")
		return nil
	}
}

func newLogger(w io.Writer, cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.Development() {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
