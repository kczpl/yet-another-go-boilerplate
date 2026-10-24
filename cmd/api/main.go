// Package main is the api binary. The run function owns startup and takes
// its environment as arguments, so tests can call it. All feature wiring
// lives in internal/app.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kczpl/yet-another-go-boilerplate/internal/app"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/config"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/database"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/logging"
	"github.com/kczpl/yet-another-go-boilerplate/internal/user"
	"github.com/kczpl/yet-another-go-boilerplate/migrations"
)

func main() {
	ctx := context.Background()
	if err := run(ctx, os.Args, os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// command is a subcommand. It runs after the migrations, and then the
// process exits.
type command func(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, stdout io.Writer) error

func run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	cmd, err := parseCommand(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.Load(getenv)
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	format := logging.JSON
	if cfg.Development() {
		format = logging.Text
	}
	logger := logging.New(stdout, cfg.LogLevel, format)

	pool, err := database.Connect(ctx, cfg.DatabaseURL, logger)
	if err != nil {
		return fmt.Errorf("connecting to postgres: %w", err)
	}
	defer pool.Close()

	if err := database.Migrate(ctx, pool, migrations.FS); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	if cmd != nil {
		return cmd(ctx, pool, logger, stdout)
	}
	return serve(ctx, cfg, logger, pool)
}

// parseCommand checks the command name and the argument count before any
// configuration or database work. It returns a nil command when args name
// no subcommand: run then starts the server.
func parseCommand(args []string) (command, error) {
	if len(args) <= 1 {
		return nil, nil
	}
	switch args[1] {
	case "migrate":
		if len(args) != 2 {
			return nil, errors.New("usage: api migrate")
		}
		// run applies the migrations before every command.
		return func(_ context.Context, _ *pgxpool.Pool, logger *slog.Logger, _ io.Writer) error {
			logger.Info("migrations applied")
			return nil
		}, nil
	case "adduser":
		if len(args) != 4 {
			return nil, errors.New("usage: api adduser <email> <name>")
		}
		email, name := args[2], args[3]
		return func(ctx context.Context, pool *pgxpool.Pool, _ *slog.Logger, stdout io.Writer) error {
			return addUser(ctx, pool, email, name, stdout)
		}, nil
	default:
		return nil, fmt.Errorf("unknown command %q (expected migrate or adduser)", args[1])
	}
}

// serve runs the HTTP server until ctx ends or a listener fails. Then it
// drains the open requests.
func serve(ctx context.Context, cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool) error {
	models, err := app.NewModels(cfg)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           app.New(logger, cfg, pool, models),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	defer srv.Close()

	// Each listener sends at most one error.
	errCh := make(chan error, 2)
	if cfg.DebugAddr != "" {
		debugSrv, err := startDebugServer(cfg.DebugAddr, errCh)
		if err != nil {
			return err
		}
		defer debugSrv.Close()
		logger.Info("debug server started", "addr", cfg.DebugAddr)
	}
	go func() { errCh <- srv.ListenAndServe() }()
	logger.Info("server started", "addr", srv.Addr, "env", cfg.Env)

	select {
	case err := <-errCh:
		return fmt.Errorf("listening and serving: %w", err)
	case <-ctx.Done():
		// The signal context is already canceled. Shutdown needs its own deadline.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutting down: %w", err)
		}
		logger.Info("server stopped")
		return nil
	}
}

// addUser creates an account and prints a random temporary password once.
// There is no register page — this command is the way to create accounts.
// It never accepts a password argument: argv leaks into the shell history
// and the process list.
func addUser(ctx context.Context, pool *pgxpool.Pool, email, name string, stdout io.Writer) error {
	// 18 random bytes encode to 24 base64url characters.
	raw := make([]byte, 18)
	rand.Read(raw) // crypto/rand.Read never returns an error (Go 1.24+).
	password := base64.RawURLEncoding.EncodeToString(raw)

	users := user.NewService(user.NewRepo(pool))
	u, err := users.Register(ctx, email, name, password)
	if err != nil {
		return fmt.Errorf("creating user: %w", err)
	}

	fmt.Fprintln(stdout, "user created")
	fmt.Fprintf(stdout, "  id:    %s\n", u.ID)
	fmt.Fprintf(stdout, "  email: %s\n", u.Email)
	fmt.Fprintf(stdout, "  temporary password (shown once): %s\n", password)
	return nil
}
