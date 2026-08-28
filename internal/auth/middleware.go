package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/logging"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/web"
)

// LoadIdentity resolves the session once per request and puts the Identity
// in the context. Anonymous requests pass through unchanged.
func (s *Service) LoadIdentity(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, err := s.Identify(r.Context(), r)
		switch {
		case err == nil:
			ctx := context.WithValue(r.Context(), contextKey{}, identity)
			// Later log records in this request carry the user id.
			ctx = logging.WithAttrs(ctx, slog.String("user_id", identity.UserID))
			r = r.WithContext(ctx)
		case errors.Is(err, ErrNoSession):
			// Clear a stale cookie, so the browser does not send it again.
			if _, cookieErr := r.Cookie(cookieName); cookieErr == nil {
				http.SetCookie(w, s.cookie("", -1))
			}
		default:
			// An infrastructure failure must not take every page down.
			// Log it and continue as anonymous.
			logger.ErrorContext(r.Context(), "loading identity", "error", err)
		}
		next.ServeHTTP(w, r)
	})
}

// RequireIdentity rejects anonymous requests. Browsers get a redirect to
// /login. htmx requests get 401 with HX-Redirect, because htmx would swap
// a plain redirect into the fragment target.
func RequireIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := IdentityFromContext(r.Context()); !ok {
			if web.IsHTMX(r) {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}
