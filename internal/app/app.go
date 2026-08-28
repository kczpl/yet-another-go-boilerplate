// Package app assembles the HTTP handler from all features. It is a
// package, not code in main, so tests build the same handler that
// production runs.
package app

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kczpl/yet-another-go-boilerplate/internal/auth"
	"github.com/kczpl/yet-another-go-boilerplate/internal/note"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/config"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/web"
	"github.com/kczpl/yet-another-go-boilerplate/internal/user"
)

const sessionTTL = 7 * 24 * time.Hour

// New wires every feature and returns the complete HTTP handler. This is
// the only wiring point: register a new feature here — construct its
// service, then call its Routes below.
func New(logger *slog.Logger, cfg config.Config, pool *pgxpool.Pool) http.Handler {
	users := user.NewService(user.NewRepo(pool))
	notes := note.NewService(note.NewRepo(pool))
	sessions := auth.NewService(auth.NewRepo(pool), sessionTTL, !cfg.Development())

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", handleHealthz(pool))
	mux.Handle("GET /static/", web.Static())

	auth.Routes(mux, logger, sessions)
	user.Routes(mux, logger, users, sessions)
	note.Routes(mux, logger, notes)

	// The pattern "/" catches every request that no other route matches.
	// The unified 404 replaces the stdlib text response.
	mux.Handle("/", web.E(logger, func(w http.ResponseWriter, r *http.Request) error {
		return web.NotFound("this page does not exist")
	}))

	// Wrap the middleware, innermost first. CrossOriginProtection (stdlib)
	// is the CSRF guard: it rejects cross-origin non-GET requests by their
	// Sec-Fetch-Site and Origin headers, so no tokens are necessary. Do not
	// remove it.
	var handler http.Handler = mux
	handler = sessions.LoadIdentity(logger, handler)
	handler = http.NewCrossOriginProtection().Handler(handler)
	handler = web.RecoverPanics(logger, handler)
	handler = web.LogRequests(logger, handler)
	return handler
}

// handleHealthz reports that the service and the database are alive.
func handleHealthz(pool *pgxpool.Pool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			http.Error(w, "database unreachable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
}
