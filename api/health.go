package api

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// handleHealthz reports liveness and database reachability. It is public (no
// auth) and skipped by the request logger.
func handleHealthz(pool *pgxpool.Pool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			respondError(w, http.StatusServiceUnavailable, "database unreachable", nil)
			return
		}
		encode(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}
