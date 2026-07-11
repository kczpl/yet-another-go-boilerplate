// Package api is the HTTP layer: server construction, routing, middleware,
// JSON encoding/decoding, request validation, and handlers. It translates
// between HTTP and the domain packages and contains no business logic.
package api

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kczpl/yet-another-go-boilerplate/config"
	"github.com/kczpl/yet-another-go-boilerplate/notes"
)

// NewServer wires every dependency into a single http.Handler. The argument
// list is intentionally explicit: adding a dependency here forces every
// caller (main, tests) to provide it, and tests get the exact same server as
// production.
func NewServer(
	logger *slog.Logger,
	cfg config.Config,
	pool *pgxpool.Pool,
	notesService *notes.Service,
) http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, logger, cfg, pool, notesService)

	var handler http.Handler = mux
	handler = recoverPanics(logger, handler)
	handler = logRequests(logger, handler)
	return handler
}
