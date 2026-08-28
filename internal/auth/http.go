package auth

import (
	"log/slog"
	"net/http"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/web"
)

// Routes registers the endpoints that auth owns. Only logout is pure
// session work; login lives in the user feature.
func Routes(mux *http.ServeMux, logger *slog.Logger, sessions *Service) {
	mux.Handle("POST /logout", handleLogout(logger, sessions))
}

func handleLogout(logger *slog.Logger, sessions *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := sessions.End(r.Context(), w, r); err != nil {
			web.InternalError(logger, w, r, err)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
}
