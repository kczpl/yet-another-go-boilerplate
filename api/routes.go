package api

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kczpl/yet-another-go-boilerplate/auth"
	"github.com/kczpl/yet-another-go-boilerplate/config"
	"github.com/kczpl/yet-another-go-boilerplate/notes"
)

// addRoutes is the single map of the whole API surface. Protected routes are
// wrapped in auth middleware here — handlers never check auth themselves.
func addRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	cfg config.Config,
	pool *pgxpool.Pool,
	notesService *notes.Service,
) {
	authed := auth.RequireAPIKey(cfg.APIKey)

	mux.Handle("GET /healthz", handleHealthz(pool))

	mux.Handle("POST /api/v1/notes", authed(handleNotesCreate(logger, notesService)))
	mux.Handle("GET /api/v1/notes", authed(handleNotesList(logger, notesService)))
	mux.Handle("GET /api/v1/notes/{id}", authed(handleNotesGet(logger, notesService)))
	mux.Handle("DELETE /api/v1/notes/{id}", authed(handleNotesDelete(logger, notesService)))

	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respondError(w, http.StatusNotFound, "not found", nil)
	}))
}
