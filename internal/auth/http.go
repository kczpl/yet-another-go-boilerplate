package auth

import (
	"log/slog"
	"net/http"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/web"
)

// Routes registers the endpoints that auth owns. Only logout is pure
// session work; login lives in the user feature.
func Routes(mux *http.ServeMux, logger *slog.Logger, sessions *Service) {
	mux.Handle("POST /logout", web.E(logger, handleLogout(sessions)))
}

func handleLogout(sessions *Service) web.HandlerE {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := sessions.End(r.Context(), w, r); err != nil {
			return err
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return nil
	}
}
