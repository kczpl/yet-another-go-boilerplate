package app_test

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var (
	noteIDPattern    = regexp.MustCompile(`hx-post="/notes/([0-9a-f-]{36})/delete"`)
	olderLinkPattern = regexp.MustCompile(`href="(/notes\?after=[^"]+)"`)
	pageCursorField  = regexp.MustCompile(`name="after" value="([^"]+)"`)
)

func TestNotesFlow(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	rec := c.get("/notes")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "No notes yet")

	// Add the note via htmx. The fragment comes back with the new note.
	rec = c.htmxPost("/notes", url.Values{
		"text": {"Buy milk"},
	})
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Buy milk")
	wantFragment(t, rec)

	match := noteIDPattern.FindStringSubmatch(rec.Body.String())
	if match == nil {
		t.Fatalf("no delete note id in body:\n%s", rec.Body.String())
	}
	noteID := match[1]

	// Delete the note via htmx. The note is gone, and the empty state is
	// back.
	rec = c.htmxPost("/notes/"+noteID+"/delete", nil)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "No notes yet")

	// A second delete, or a delete with a garbage id, causes a calm
	// re-render, not an error.
	rec = c.htmxPost("/notes/"+noteID+"/delete", nil)
	wantStatus(t, rec, http.StatusOK)
	rec = c.htmxPost("/notes/not-a-uuid/delete", nil)
	wantStatus(t, rec, http.StatusOK)
}

func TestNoteDeleteNoJS(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	// Add the note with a plain form post (PRG).
	rec := c.postForm("/notes", url.Values{"text": {"Buy milk"}})
	wantStatus(t, rec, http.StatusSeeOther)

	rec = c.get("/notes")
	wantStatus(t, rec, http.StatusOK)
	match := noteIDPattern.FindStringSubmatch(rec.Body.String())
	if match == nil {
		t.Fatalf("no delete note id in body:\n%s", rec.Body.String())
	}

	// The delete form posts without JavaScript and redirects back (PRG).
	rec = c.postForm("/notes/"+match[1]+"/delete", nil)
	wantStatus(t, rec, http.StatusSeeOther)
	wantLocation(t, rec, "/notes")
	rec = c.get("/notes")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "No notes yet")
}

func TestNotesPagination(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	// One page holds 20 notes (pageSize in the note service).
	for i := range 21 {
		wantStatus(t, c.htmxPost("/notes", url.Values{"text": {fmt.Sprintf("note-%02d", i+1)}}), http.StatusOK)
	}

	first := c.get("/notes")
	wantStatus(t, first, http.StatusOK)
	wantContains(t, first, "note-21")
	if strings.Contains(first.Body.String(), "note-01") {
		t.Fatal("the first page contains the oldest note")
	}
	if strings.Contains(first.Body.String(), "Newest notes") {
		t.Fatal("the first page links to itself")
	}
	link := olderLinkPattern.FindStringSubmatch(first.Body.String())
	if link == nil {
		t.Fatalf("no link to older notes:\n%s", first.Body.String())
	}

	second := c.get(html.UnescapeString(link[1]))
	wantStatus(t, second, http.StatusOK)
	wantContains(t, second, "note-01")
	wantContains(t, second, "Newest notes")
	if strings.Contains(second.Body.String(), "note-02") || strings.Contains(second.Body.String(), "Older notes") {
		t.Fatalf("the last page shows more than the oldest note:\n%s", second.Body.String())
	}

	// A delete on the second page redirects back to the second page.
	id := noteIDPattern.FindStringSubmatch(second.Body.String())
	cursor := pageCursorField.FindStringSubmatch(second.Body.String())
	if id == nil || cursor == nil {
		t.Fatalf("no delete form with a page cursor:\n%s", second.Body.String())
	}
	rec := c.postForm("/notes/"+id[1]+"/delete", url.Values{"after": {html.UnescapeString(cursor[1])}})
	wantStatus(t, rec, http.StatusSeeOther)
	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, "/notes?after=") {
		t.Fatalf("Location = %q, want the second page", location)
	}
	wantContains(t, c.get(location), "No older notes")
}

func TestNotesRejectBadCursor(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	for _, after := range []string{"garbage", "2026-01-01T00:00:00Z_not-a-uuid", "yesterday_0190a7e0-0000-7000-8000-000000000000"} {
		rec := c.get("/notes?after=" + url.QueryEscape(after))
		wantStatus(t, rec, http.StatusBadRequest)
		wantContains(t, rec, "invalid page cursor")
	}
}

func TestNoteAddValidation(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	// The textarea carries `required`, but the server must not trust the
	// client. Blank text must fail on the htmx path with a 422 fragment.
	rec := c.htmxPost("/notes", url.Values{
		"text": {"   "},
	})
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "text must not be empty")

	// The no-JS path returns a full page with the error.
	rec = c.postForm("/notes", url.Values{"text": {""}})
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "text must not be empty")
	wantPage(t, rec)
}
