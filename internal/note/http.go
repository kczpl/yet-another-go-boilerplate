package note

import (
	"context"
	"embed"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kczpl/yet-another-go-boilerplate/internal/auth"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/web"
)

//go:embed templates/*.html
var templatesFS embed.FS

// notesTmpl holds the page template. The package parses it at startup
// and does not change it.
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
	// After is the cursor of this page. It is empty on the first page.
	After string
	// Older is the cursor of the next page. It is empty on the last page.
	Older string
}

func handleNotes(svc *Service) web.HandlerE {
	return func(w http.ResponseWriter, r *http.Request) error {
		after, err := parseCursor(r.URL.Query().Get("after"))
		if err != nil {
			return web.BadRequest("invalid page cursor")
		}
		identity, _ := auth.IdentityFromContext(r.Context())
		data, err := loadNotes(r.Context(), svc, identity.UserID, after)
		if err != nil {
			return err
		}
		// htmx (the /me dashboard embed) gets the bare fragment. Browsers
		// get the full page.
		return renderNotes(w, r, http.StatusOK, data)
	}
}

func handleNoteAdd(svc *Service) web.HandlerE {
	return func(w http.ResponseWriter, r *http.Request) error {
		// A malformed or too-large body is a client fault, not a 500.
		if err := r.ParseForm(); err != nil {
			return web.BadRequest("invalid form data")
		}
		identity, _ := auth.IdentityFromContext(r.Context())
		form := noteForm{Text: r.PostFormValue("text")}

		_, err := svc.Add(r.Context(), identity.UserID, form.Text)
		var vErr ValidationError
		if errors.As(err, &vErr) {
			data, err := loadNotes(r.Context(), svc, identity.UserID, Cursor{})
			if err != nil {
				return err
			}
			data.Form = form
			data.Error = vErr.Error()
			return renderNotes(w, r, http.StatusUnprocessableEntity, data)
		}
		if err != nil {
			return err
		}
		// The new note is the newest, so the first page shows it.
		return notesChanged(w, r, svc, identity.UserID, Cursor{})
	}
}

func handleNoteDelete(svc *Service) web.HandlerE {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := r.ParseForm(); err != nil {
			return web.BadRequest("invalid form data")
		}
		// The form sends the cursor of its page, so the user stays on
		// that page.
		after, err := parseCursor(r.PostFormValue("after"))
		if err != nil {
			return web.BadRequest("invalid page cursor")
		}
		identity, _ := auth.IdentityFromContext(r.Context())

		// A malformed id is an unknown note. It never reaches the uuid
		// cast. ErrNotFound is not an error here: a repeated delete shows
		// the same page again (idempotent UX).
		if id := r.PathValue("id"); isUUID(id) {
			err := svc.Delete(r.Context(), identity.UserID, id)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
		}
		return notesChanged(w, r, svc, identity.UserID, after)
	}
}

// loadNotes builds the page data for the page of userID's notes that
// starts after the cursor.
func loadNotes(ctx context.Context, svc *Service, userID string, after Cursor) (notesData, error) {
	notes, next, err := svc.List(ctx, userID, after)
	if err != nil {
		return notesData{}, err
	}
	return notesData{
		Page:  web.Page{Title: "My notes", LoggedIn: true},
		Notes: notes,
		After: formatCursor(after),
		Older: formatCursor(next),
	}, nil
}

// renderNotes writes the notes section for htmx and the full page for
// all other clients.
func renderNotes(w http.ResponseWriter, r *http.Request, status int, data notesData) error {
	if web.IsHTMX(r) {
		return web.RenderFragment(w, status, notesTmpl, "notes-section", data)
	}
	return web.RenderPage(w, status, notesTmpl, data)
}

// notesChanged answers a successful change. htmx gets the new notes
// section. A plain browser gets a redirect to the same page (PRG), so the
// flow works without JavaScript.
func notesChanged(w http.ResponseWriter, r *http.Request, svc *Service, userID string, after Cursor) error {
	if !web.IsHTMX(r) {
		target := "/notes"
		if !after.IsZero() {
			target += "?after=" + url.QueryEscape(formatCursor(after))
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return nil
	}
	data, err := loadNotes(r.Context(), svc, userID, after)
	if err != nil {
		return err
	}
	return renderNotes(w, r, http.StatusOK, data)
}

// formatCursor encodes c for a URL as <created_at>_<id>. The time keeps
// all its digits, so the page boundary stays exact. A zero c gives "".
func formatCursor(c Cursor) string {
	if c.IsZero() {
		return ""
	}
	return c.CreatedAt.UTC().Format(time.RFC3339Nano) + "_" + c.ID
}

// parseCursor decodes the output of formatCursor. An empty s is the start
// of the list. A client can send any text, so parseCursor also validates
// the id before it reaches the uuid cast.
func parseCursor(s string) (Cursor, error) {
	if s == "" {
		return Cursor{}, nil
	}
	createdAt, id, ok := strings.Cut(s, "_")
	if !ok || !isUUID(id) {
		return Cursor{}, errors.New("malformed cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Cursor{}, err
	}
	return Cursor{CreatedAt: t, ID: id}, nil
}

// isUUID checks the canonical 8-4-4-4-12 form. It keeps garbage out of the
// uuid cast. Postgres remains the authority.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for _, i := range []int{8, 13, 18, 23} {
		if s[i] != '-' {
			return false
		}
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	return err == nil && len(decoded) == 16
}
