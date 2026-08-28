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
	mux.Handle("GET /notes", auth.RequireIdentity(web.E(logger, handleNotes(svc))))
	mux.Handle("POST /notes", auth.RequireIdentity(web.E(logger, handleNoteAdd(svc))))
	// Delete is a POST, not a DELETE: a plain HTML form can only send GET
	// and POST, and every flow must work without JavaScript.
	mux.Handle("POST /notes/{id}/delete", auth.RequireIdentity(web.E(logger, handleNoteDelete(svc))))
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

func handleNotes(svc *Service) web.HandlerE {
	return func(w http.ResponseWriter, r *http.Request) error {
		identity, _ := auth.IdentityFromContext(r.Context())
		notes, err := svc.List(r.Context(), identity.UserID)
		if err != nil {
			return err
		}
		// htmx (the /me dashboard embed) gets the bare fragment. Browsers
		// get the full page.
		if web.IsHTMX(r) {
			return web.RenderFragment(w, http.StatusOK, notesTmpl, "notes-section", newNotesData(notes))
		}
		return web.RenderPage(w, http.StatusOK, notesTmpl, newNotesData(notes))
	}
}

func handleNoteAdd(svc *Service) web.HandlerE {
	return func(w http.ResponseWriter, r *http.Request) error {
		// A malformed or too-large body is a client fault, not a 500.
		if err := r.ParseForm(); err != nil {
			return web.BadRequest("invalid form data")
		}
		identity, _ := auth.IdentityFromContext(r.Context())
		form := noteForm{Text: r.FormValue("text")}

		_, err := svc.Add(r.Context(), identity.UserID, form.Text)
		var vErr ValidationError
		if errors.As(err, &vErr) {
			return respondNotes(w, r, svc, identity.UserID, form, vErr.Error())
		}
		if err != nil {
			return err
		}
		return respondNotes(w, r, svc, identity.UserID, noteForm{}, "")
	}
}

func handleNoteDelete(svc *Service) web.HandlerE {
	return func(w http.ResponseWriter, r *http.Request) error {
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
			return err
		}
		return respondNotes(w, r, svc, identity.UserID, noteForm{}, "")
	}
}

// respondNotes re-renders the notes section. htmx gets the fragment. Plain
// browsers get a redirect on success and a full 422 page on an error, so
// the flow works without JavaScript.
func respondNotes(w http.ResponseWriter, r *http.Request, svc *Service, userID string, form noteForm, errMsg string) error {
	notes, err := svc.List(r.Context(), userID)
	if err != nil {
		return err
	}
	data := newNotesData(notes)
	data.Form = form
	data.Error = errMsg

	status := http.StatusOK
	if errMsg != "" {
		status = http.StatusUnprocessableEntity
	}
	if web.IsHTMX(r) {
		return web.RenderFragment(w, status, notesTmpl, "notes-section", data)
	}
	if errMsg == "" {
		http.Redirect(w, r, "/notes", http.StatusSeeOther)
		return nil
	}
	return web.RenderPage(w, status, notesTmpl, data)
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
