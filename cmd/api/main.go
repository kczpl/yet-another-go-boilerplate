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

func run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.Load(getenv)
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	logger := logging.New(stdout, cfg)

	pool, err := database.Connect(ctx, cfg.DatabaseURL, logger)
	if err != nil {
		return fmt.Errorf("connecting to postgres: %w", err)
	}
	defer pool.Close()

	if err := database.Migrate(ctx, pool, migrations.FS); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	// A subcommand runs after the migrations and exits. Without a
	// subcommand, run starts the server.
	if len(args) > 1 {
		switch args[1] {
		case "migrate":
			logger.Info("migrations applied")
			return nil
		case "adduser":
			return addUser(ctx, pool, args[2:], stdout)
		default:
			return fmt.Errorf("unknown command %q (expected migrate or adduser)", args[1])
		}
	}

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           app.New(logger, cfg, pool),
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
func addUser(ctx context.Context, pool *pgxpool.Pool, args []string, stdout io.Writer) error {
	if len(args) != 2 {
		return errors.New("usage: api adduser <email> <name>")
	}
	email, name := args[0], args[1]

	// 18 random bytes encode to 24 base64url characters.
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("generating password: %w", err)
	}
	password := base64.RawURLEncoding.EncodeToString(raw)

	users := user.NewService(user.NewRepo(pool))
	u, err := users.Register(ctx, email, name, password)
	if err != nil {
		return fmt.Errorf("creating user: %w", err)
	}

	fmt.Fprintf(stdout, "user created\n")
	fmt.Fprintf(stdout, "  id:    %s\n", u.ID)
	fmt.Fprintf(stdout, "  email: %s\n", u.Email)
	fmt.Fprintf(stdout, "  temporary password (shown once): %s\n", password)
	return nil
}
