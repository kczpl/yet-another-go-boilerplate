package user

import (
	"embed"
	"errors"
	"log/slog"
	"net/http"

	"github.com/kczpl/yet-another-go-boilerplate/internal/auth"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/web"
)

//go:embed templates/*.html
var templatesFS embed.FS

// One immutable template set per page, parsed at startup. MustPage panics
// on a bad template, so it fails at startup, never on the first request.
var (
	loginTmpl = web.MustPage(templatesFS, "templates/login.html")
	meTmpl    = web.MustPage(templatesFS, "templates/me.html")
)

// Routes registers every endpoint of the feature. Login lives here, not in
// auth, because it needs Service. There is no register page — create
// accounts with Service.Register from code you control.
func Routes(mux *http.ServeMux, logger *slog.Logger, svc *Service, sessions *auth.Service) {
	mux.Handle("GET /{$}", handleHome())
	mux.Handle("GET /login", handleLoginPage(logger))
	mux.Handle("POST /login", handleLogin(logger, svc, sessions))
	mux.Handle("GET /me", auth.RequireIdentity(handleMe(logger, svc)))
	mux.Handle("POST /me", auth.RequireIdentity(handleMeUpdate(logger, svc)))
}

type loginData struct {
	web.Page
	Email string
	Error string
}

type profileForm struct {
	Email string
	Name  string
}

type meData struct {
	web.Page
	User  User
	Form  profileForm
	Error string
	Saved bool
}

func handleHome() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.IdentityFromContext(r.Context()); ok {
			http.Redirect(w, r, "/me", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
}

func handleLoginPage(logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.IdentityFromContext(r.Context()); ok {
			http.Redirect(w, r, "/me", http.StatusSeeOther)
			return
		}
		web.RenderPage(r.Context(), logger, w, http.StatusOK, loginTmpl, loginData{
			Page: web.Page{Title: "Log in"},
		})
	})
}

func handleLogin(logger *slog.Logger, svc *Service, sessions *auth.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		email := r.FormValue("email")

		u, err := svc.Authenticate(r.Context(), email, r.FormValue("password"))
		if errors.Is(err, ErrInvalidCredentials) {
			web.RenderPage(r.Context(), logger, w, http.StatusUnauthorized, loginTmpl, loginData{
				Page:  web.Page{Title: "Log in"},
				Email: email,
				Error: err.Error(),
			})
			return
		}
		if err != nil {
			web.InternalError(logger, w, r, err)
			return
		}
		if err := sessions.Start(r.Context(), w, u.ID); err != nil {
			web.InternalError(logger, w, r, err)
			return
		}
		http.Redirect(w, r, "/me", http.StatusSeeOther)
	})
}

func handleMe(logger *slog.Logger, svc *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, _ := auth.IdentityFromContext(r.Context())
		u, err := svc.Get(r.Context(), identity.UserID)
		if err != nil {
			web.InternalError(logger, w, r, err)
			return
		}
		web.RenderPage(r.Context(), logger, w, http.StatusOK, meTmpl, meData{
			Page: web.Page{Title: "Home", LoggedIn: true},
			User: u,
			Form: profileForm{Email: u.Email, Name: u.Name},
			// The PRG flash: the no-JS update redirects to /me?saved=1.
			Saved: r.URL.Query().Get("saved") == "1",
		})
	})
}

func handleMeUpdate(logger *slog.Logger, svc *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, _ := auth.IdentityFromContext(r.Context())
		form := profileForm{
			Email: r.FormValue("email"),
			Name:  r.FormValue("name"),
		}

		u, err := svc.UpdateProfile(r.Context(), identity.UserID, form.Email, form.Name)
		if err != nil {
			msg, ok := formError(err)
			if !ok {
				web.InternalError(logger, w, r, err)
				return
			}
			// Re-render with the current user, but keep the submitted form values.
			current, getErr := svc.Get(r.Context(), identity.UserID)
			if getErr != nil {
				web.InternalError(logger, w, r, getErr)
				return
			}
			data := meData{
				Page:  web.Page{Title: "Home", LoggedIn: true},
				User:  current,
				Form:  form,
				Error: msg,
			}
			if web.IsHTMX(r) {
				web.RenderFragment(r.Context(), logger, w, http.StatusUnprocessableEntity, meTmpl, "profile-section", data)
				return
			}
			web.RenderPage(r.Context(), logger, w, http.StatusUnprocessableEntity, meTmpl, data)
			return
		}

		if web.IsHTMX(r) {
			web.RenderFragment(r.Context(), logger, w, http.StatusOK, meTmpl, "profile-section", meData{
				Page:  web.Page{Title: "Home", LoggedIn: true},
				User:  u,
				Form:  profileForm{Email: u.Email, Name: u.Name},
				Saved: true,
			})
			return
		}
		// Redirect, so a refresh does not resubmit the form (PRG).
		http.Redirect(w, r, "/me?saved=1", http.StatusSeeOther)
	})
}

// formError maps service errors to a message that is safe to show in the
// form. The value ok is false for internal errors.
func formError(err error) (msg string, ok bool) {
	var vErr ValidationError
	if errors.As(err, &vErr) {
		return vErr.Error(), true
	}
	if errors.Is(err, ErrEmailTaken) {
		return ErrEmailTaken.Error(), true
	}
	return "", false
}
