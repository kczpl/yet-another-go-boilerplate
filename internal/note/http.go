package note

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

// notesTmpl is parsed once at startup. Immutable template sets are the one
// allowed package-level variable.
var notesTmpl = web.MustPage(templatesFS, "templates/notes.html")

// Routes registers every endpoint of the feature. Add new routes here and
// wrap them with auth.RequireIdentity.
func Routes(mux *http.ServeMux, logger *slog.Logger, svc *Service) {
	mux.Handle("GET /notes", auth.RequireIdentity(handleNotes(logger, svc)))
	mux.Handle("POST /notes", auth.RequireIdentity(handleNoteAdd(logger, svc)))
	mux.Handle("DELETE /notes/{id}", auth.RequireIdentity(handleNoteDelete(logger, svc)))
}

type noteForm struct {
	Text string
}

type notesData struct {
	web.Page
	Notes []Note
	Form  noteForm
	Error string
}

func newNotesData(notes []Note) notesData {
	return notesData{
		Page:  web.Page{Title: "My notes", LoggedIn: true},
		Notes: notes,
	}
}

func handleNotes(logger *slog.Logger, svc *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, _ := auth.IdentityFromContext(r.Context())
		notes, err := svc.List(r.Context(), identity.UserID)
		if err != nil {
			web.InternalError(logger, w, r, err)
			return
		}
		// htmx (the /me dashboard embed) gets the bare fragment. Browsers
		// get the full page.
		if web.IsHTMX(r) {
			web.RenderFragment(r.Context(), logger, w, http.StatusOK, notesTmpl, "notes-section", newNotesData(notes))
			return
		}
		web.RenderPage(r.Context(), logger, w, http.StatusOK, notesTmpl, newNotesData(notes))
	})
}

func handleNoteAdd(logger *slog.Logger, svc *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, _ := auth.IdentityFromContext(r.Context())
		form := noteForm{Text: r.FormValue("text")}

		_, err := svc.Add(r.Context(), identity.UserID, form.Text)
		var vErr ValidationError
		if errors.As(err, &vErr) {
			respondNotes(logger, w, r, svc, identity.UserID, form, vErr.Error())
			return
		}
		if err != nil {
			web.InternalError(logger, w, r, err)
			return
		}
		respondNotes(logger, w, r, svc, identity.UserID, noteForm{}, "")
	})
}

func handleNoteDelete(logger *slog.Logger, svc *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, _ := auth.IdentityFromContext(r.Context())
		id := r.PathValue("id")

		// Treat a malformed id as an unknown note, not as a uuid cast error.
		var err error
		if !isUUID(id) {
			err = ErrNotFound
		} else {
			err = svc.Delete(r.Context(), identity.UserID, id)
		}
		// ErrNotFound falls through to the re-render: a repeated delete is
		// not an error (idempotent UX).
		if err != nil && !errors.Is(err, ErrNotFound) {
			web.InternalError(logger, w, r, err)
			return
		}
		respondNotes(logger, w, r, svc, identity.UserID, noteForm{}, "")
	})
}

// respondNotes re-renders the notes section. htmx gets the fragment. Plain
// browsers get a redirect on success and a full 422 page on an error, so
// the flow works without JavaScript.
func respondNotes(logger *slog.Logger, w http.ResponseWriter, r *http.Request, svc *Service, userID string, form noteForm, errMsg string) {
	notes, err := svc.List(r.Context(), userID)
	if err != nil {
		web.InternalError(logger, w, r, err)
		return
	}
	data := newNotesData(notes)
	data.Form = form
	data.Error = errMsg

	status := http.StatusOK
	if errMsg != "" {
		status = http.StatusUnprocessableEntity
	}
	if web.IsHTMX(r) {
		web.RenderFragment(r.Context(), logger, w, status, notesTmpl, "notes-section", data)
		return
	}
	if errMsg == "" {
		http.Redirect(w, r, "/notes", http.StatusSeeOther)
		return
	}
	web.RenderPage(r.Context(), logger, w, status, notesTmpl, data)
}

// isUUID checks the canonical 8-4-4-4-12 form. It keeps garbage out of the
// uuid cast. Postgres remains the authority.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}
