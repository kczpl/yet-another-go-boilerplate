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
	mux.Handle("GET /{$}", web.E(logger, handleHome()))
	mux.Handle("GET /login", web.E(logger, handleLoginPage()))
	mux.Handle("POST /login", web.E(logger, handleLogin(svc, sessions)))
	mux.Handle("GET /me", auth.RequireIdentity(web.E(logger, handleMe(svc))))
	mux.Handle("POST /me", auth.RequireIdentity(web.E(logger, handleMeUpdate(svc))))
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

func handleHome() web.HandlerE {
	return func(w http.ResponseWriter, r *http.Request) error {
		if _, ok := auth.IdentityFromContext(r.Context()); ok {
			http.Redirect(w, r, "/me", http.StatusSeeOther)
			return nil
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return nil
	}
}

func handleLoginPage() web.HandlerE {
	return func(w http.ResponseWriter, r *http.Request) error {
		if _, ok := auth.IdentityFromContext(r.Context()); ok {
			http.Redirect(w, r, "/me", http.StatusSeeOther)
			return nil
		}
		return web.RenderPage(w, http.StatusOK, loginTmpl, loginData{
			Page: web.Page{Title: "Log in"},
		})
	}
}

func handleLogin(svc *Service, sessions *auth.Service) web.HandlerE {
	return func(w http.ResponseWriter, r *http.Request) error {
		// A malformed or too-large body is a client fault, not a 500.
		if err := r.ParseForm(); err != nil {
			return web.BadRequest("invalid form data")
		}
		email := r.FormValue("email")

		u, err := svc.Authenticate(r.Context(), email, r.FormValue("password"))
		if errors.Is(err, ErrInvalidCredentials) {
			return web.RenderPage(w, http.StatusUnauthorized, loginTmpl, loginData{
				Page:  web.Page{Title: "Log in"},
				Email: email,
				Error: err.Error(),
			})
		}
		if err != nil {
			return err
		}
		if err := sessions.Start(r.Context(), w, u.ID); err != nil {
			return err
		}
		http.Redirect(w, r, "/me", http.StatusSeeOther)
		return nil
	}
}

func handleMe(svc *Service) web.HandlerE {
	return func(w http.ResponseWriter, r *http.Request) error {
		identity, _ := auth.IdentityFromContext(r.Context())
		u, err := svc.Get(r.Context(), identity.UserID)
		if err != nil {
			return err
		}
		return web.RenderPage(w, http.StatusOK, meTmpl, meData{
			Page: web.Page{Title: "Home", LoggedIn: true},
			User: u,
			Form: profileForm{Email: u.Email, Name: u.Name},
			// The PRG flash: the no-JS update redirects to /me?saved=1.
			Saved: r.URL.Query().Get("saved") == "1",
		})
	}
}

func handleMeUpdate(svc *Service) web.HandlerE {
	return func(w http.ResponseWriter, r *http.Request) error {
		// A malformed or too-large body is a client fault, not a 500.
		if err := r.ParseForm(); err != nil {
			return web.BadRequest("invalid form data")
		}
		identity, _ := auth.IdentityFromContext(r.Context())
		form := profileForm{
			Email: r.FormValue("email"),
			Name:  r.FormValue("name"),
		}

		u, err := svc.UpdateProfile(r.Context(), identity.UserID, form.Email, form.Name)
		if err != nil {
			msg, ok := formError(err)
			if !ok {
				return err
			}
			// Re-render with the current user, but keep the submitted form values.
			current, getErr := svc.Get(r.Context(), identity.UserID)
			if getErr != nil {
				return getErr
			}
			data := meData{
				Page:  web.Page{Title: "Home", LoggedIn: true},
				User:  current,
				Form:  form,
				Error: msg,
			}
			if web.IsHTMX(r) {
				return web.RenderFragment(w, http.StatusUnprocessableEntity, meTmpl, "profile-section", data)
			}
			return web.RenderPage(w, http.StatusUnprocessableEntity, meTmpl, data)
		}

		if web.IsHTMX(r) {
			return web.RenderFragment(w, http.StatusOK, meTmpl, "profile-section", meData{
				Page:  web.Page{Title: "Home", LoggedIn: true},
				User:  u,
				Form:  profileForm{Email: u.Email, Name: u.Name},
				Saved: true,
			})
		}
		// Redirect, so a refresh does not resubmit the form (PRG).
		http.Redirect(w, r, "/me?saved=1", http.StatusSeeOther)
		return nil
	}
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
