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

// maxRequestBytes caps every request body. The largest legitimate body in
// this app is a small HTML form; 1 MiB leaves generous room.
const maxRequestBytes = 1 << 20

// New wires every feature and returns the complete HTTP handler. This is
// the only wiring point: register a new feature here — construct its
// service, then call its Routes below.
func New(logger *slog.Logger, cfg config.Config, pool *pgxpool.Pool) http.Handler {
	users := user.NewService(user.NewRepo(pool))
	notes := note.NewService(note.NewRepo(pool))
	sessions := auth.NewService(auth.NewRepo(pool), sessionTTL, !cfg.Development())

	// The pages mux holds every feature route. It sits behind the session
	// and CSRF middleware.
	pages := http.NewServeMux()
	auth.Routes(pages, logger, sessions)
	user.Routes(pages, logger, users, sessions)
	note.Routes(pages, logger, notes)

	// The pattern "/" catches every request that no other route matches.
	// The unified 404 replaces the stdlib text response.
	pages.Handle("/", web.E(logger, func(w http.ResponseWriter, r *http.Request) error {
		return web.NotFound("this page does not exist")
	}))

	// Wrap the page middleware, innermost first. CrossOriginProtection
	// (stdlib) is the CSRF guard: it rejects cross-origin non-GET requests
	// by their Sec-Fetch-Site and Origin headers, so no tokens are
	// necessary. Do not remove it.
	var pageHandler http.Handler = pages
	pageHandler = sessions.LoadIdentity(logger, pageHandler)
	pageHandler = http.NewCrossOriginProtection().Handler(pageHandler)

	// The root mux keeps health probes and static assets outside the
	// session and CSRF middleware: they are anonymous GETs and must not
	// cost a database lookup.
	root := http.NewServeMux()
	root.Handle("GET /healthz", handleHealthz(pool))
	root.Handle("GET /static/", web.Static())
	root.Handle("/", pageHandler)

	// The outer middleware applies to every response, static assets and
	// error pages included.
	var handler http.Handler = root
	handler = http.MaxBytesHandler(handler, maxRequestBytes)
	handler = web.SecureHeaders(handler)
	handler = web.RecoverPanics(logger, handler)
	handler = web.LogRequests(logger, handler)
	return handler
}

// handleHealthz reports that the service and the database are alive. It
// stays a plain http.Handler on purpose: load-balancer probes expect a
// plain text body, and a database outage must not spam the error log
// through web.E on every probe.
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
