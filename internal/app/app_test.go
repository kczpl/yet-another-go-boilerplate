package app_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/kczpl/yet-another-go-boilerplate/internal/app"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/config"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/logging"
	"github.com/kczpl/yet-another-go-boilerplate/internal/testdb"
	"github.com/kczpl/yet-another-go-boilerplate/internal/user"
)

// client drives the full production handler (app.New: middleware, auth,
// routes, templates) through httptest. It carries the session cookie between
// requests, like a browser.
type client struct {
	t       *testing.T
	handler http.Handler
	users   *user.Service
	cookie  *http.Cookie
}

func newClient(t *testing.T) *client {
	t.Helper()
	pool := testdb.New(t)
	cfg := config.Load(func(string) string { return "" }) // use the development defaults
	logger := logging.New(io.Discard, cfg)
	return &client{
		t:       t,
		handler: app.New(logger, cfg, pool),
		users:   user.NewService(user.NewRepo(pool)),
	}
}

func (c *client) do(method, path string, form url.Values, htmx bool) *httptest.ResponseRecorder {
	c.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}

	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)

	for _, ck := range rec.Result().Cookies() {
		if ck.Name == "session_id" {
			if ck.MaxAge < 0 {
				c.cookie = nil
			} else {
				c.cookie = ck
			}
		}
	}
	return rec
}

func (c *client) get(path string) *httptest.ResponseRecorder {
	return c.do(http.MethodGet, path, nil, false)
}

func (c *client) postForm(path string, form url.Values) *httptest.ResponseRecorder {
	return c.do(http.MethodPost, path, form, false)
}

// register creates an account through the service, because there is no
// register page. Then it logs the client in through the real POST /login
// flow.
func (c *client) register(email, name, password string) {
	c.t.Helper()
	if _, err := c.users.Register(c.t.Context(), email, name, password); err != nil {
		c.t.Fatalf("register: %v", err)
	}
	rec := c.postForm("/login", url.Values{
		"email": {email}, "password": {password},
	})
	if rec.Code != http.StatusSeeOther {
		c.t.Fatalf("login: status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	if c.cookie == nil {
		c.t.Fatal("login did not set a session cookie")
	}
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, want, rec.Body.String())
	}
}

func wantContains(t *testing.T, rec *httptest.ResponseRecorder, substr string) {
	t.Helper()
	if !strings.Contains(rec.Body.String(), substr) {
		t.Fatalf("body does not contain %q:\n%s", substr, rec.Body.String())
	}
}

func TestLoginLogoutFlow(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	rec := c.get("/me")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "bob@example.com")
	wantContains(t, rec, "Bob")

	// Logout clears the session and the cookie.
	rec = c.postForm("/logout", nil)
	wantStatus(t, rec, http.StatusSeeOther)
	if c.cookie != nil {
		t.Fatal("logout did not clear the session cookie")
	}
	rec = c.get("/me")
	wantStatus(t, rec, http.StatusSeeOther)
	if got := rec.Header().Get("Location"); got != "/login" {
		t.Fatalf("Location = %q, want /login", got)
	}

	// Log back in. The uppercase email must match: login is
	// case-insensitive.
	rec = c.postForm("/login", url.Values{
		"email": {"BOB@example.com"}, "password": {"s3cret-pass"},
	})
	wantStatus(t, rec, http.StatusSeeOther)
	rec = c.get("/me")
	wantStatus(t, rec, http.StatusOK)
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")
	c.postForm("/logout", nil)

	rec := c.postForm("/login", url.Values{
		"email": {"bob@example.com"}, "password": {"wrong-pass"},
	})
	wantStatus(t, rec, http.StatusUnauthorized)
	wantContains(t, rec, "invalid email or password")
	if c.cookie != nil {
		t.Fatal("failed login must not set a session cookie")
	}
}

func TestUnifiedErrors(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	// An unknown path renders the shared error page, not the stdlib text.
	rec := c.get("/no-such-page")
	wantStatus(t, rec, http.StatusNotFound)
	wantContains(t, rec, "<html")
	wantContains(t, rec, "404")
	wantContains(t, rec, "this page does not exist")

	// A JSON client gets the JSON error envelope instead of HTML.
	req := httptest.NewRequest(http.MethodGet, "/no-such-page", nil)
	req.Header.Set("Accept", "application/json")
	rec = httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	wantStatus(t, rec, http.StatusNotFound)
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	wantContains(t, rec, `"status":404`)
	wantContains(t, rec, `"message":"this page does not exist"`)
}

func TestNoRegisterRoute(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	// The web app has no register endpoint. Tests create accounts through
	// user.Service directly.
	rec := c.get("/register")
	wantStatus(t, rec, http.StatusNotFound)
	rec = c.postForm("/register", url.Values{
		"email": {"bob@example.com"}, "name": {"Bob"}, "password": {"s3cret-pass"},
	})
	wantStatus(t, rec, http.StatusNotFound)
}

func TestProfileUpdate(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	// This is the htmx path. The response is the bare fragment, not a full
	// page.
	rec := c.do(http.MethodPost, "/me", url.Values{
		"email": {"robert@example.com"}, "name": {"Robert"},
	}, true)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `id="profile-section"`)
	wantContains(t, rec, "Profile saved.")
	wantContains(t, rec, "robert@example.com")
	if strings.Contains(rec.Body.String(), "<html") {
		t.Fatal("htmx response contains a full page, want a fragment")
	}

	// An htmx validation error returns a 422 fragment with the message and
	// the input.
	rec = c.do(http.MethodPost, "/me", url.Values{
		"email": {"not-an-email"}, "name": {"Robert"},
	}, true)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "enter a valid email address")
	wantContains(t, rec, `value="not-an-email"`)

	// This is the no-JS path. A plain form post redirects (PRG).
	rec = c.postForm("/me", url.Values{
		"email": {"robert@example.com"}, "name": {"Bob Again"},
	})
	wantStatus(t, rec, http.StatusSeeOther)
	if got := rec.Header().Get("Location"); got != "/me?saved=1" {
		t.Fatalf("Location = %q, want /me?saved=1", got)
	}
	rec = c.get("/me?saved=1")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Profile saved.")
	wantContains(t, rec, "Bob Again")
}

func TestRequireIdentity(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	// A browser request gets a redirect to the login page.
	rec := c.get("/me")
	wantStatus(t, rec, http.StatusSeeOther)
	if got := rec.Header().Get("Location"); got != "/login" {
		t.Fatalf("Location = %q, want /login", got)
	}

	// An htmx request gets a 401 with HX-Redirect, so htmx navigates the
	// whole page.
	rec = c.do(http.MethodPost, "/me", url.Values{"name": {"x"}}, true)
	wantStatus(t, rec, http.StatusUnauthorized)
	if got := rec.Header().Get("HX-Redirect"); got != "/login" {
		t.Fatalf("HX-Redirect = %q, want /login", got)
	}
}

func TestHomeRedirects(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	rec := c.get("/")
	wantStatus(t, rec, http.StatusSeeOther)
	if got := rec.Header().Get("Location"); got != "/login" {
		t.Fatalf("anonymous Location = %q, want /login", got)
	}

	c.register("bob@example.com", "Bob", "s3cret-pass")
	rec = c.get("/")
	wantStatus(t, rec, http.StatusSeeOther)
	if got := rec.Header().Get("Location"); got != "/me" {
		t.Fatalf("logged-in Location = %q, want /me", got)
	}
}

var noteIDPattern = regexp.MustCompile(`hx-post="/notes/([0-9a-f-]{36})/delete"`)

func TestNotesFlow(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	rec := c.get("/notes")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "No notes yet")

	// Add the note via htmx. The fragment comes back with the new note.
	rec = c.do(http.MethodPost, "/notes", url.Values{
		"text": {"Buy milk"},
	}, true)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Buy milk")
	if strings.Contains(rec.Body.String(), "<html") {
		t.Fatal("htmx response contains a full page, want a fragment")
	}

	match := noteIDPattern.FindStringSubmatch(rec.Body.String())
	if match == nil {
		t.Fatalf("no delete note id in body:\n%s", rec.Body.String())
	}
	noteID := match[1]

	// Delete the note via htmx. The note is gone, and the empty state is
	// back.
	rec = c.do(http.MethodPost, "/notes/"+noteID+"/delete", nil, true)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "No notes yet")

	// A second delete, or a delete with a garbage id, causes a calm
	// re-render, not an error.
	rec = c.do(http.MethodPost, "/notes/"+noteID+"/delete", nil, true)
	wantStatus(t, rec, http.StatusOK)
	rec = c.do(http.MethodPost, "/notes/not-a-uuid/delete", nil, true)
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
	if got := rec.Header().Get("Location"); got != "/notes" {
		t.Fatalf("Location = %q, want /notes", got)
	}
	rec = c.get("/notes")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "No notes yet")
}

func TestDashboardEmbedsNotes(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	// /me is one screen: the profile section plus the section of the note
	// feature. The dashboard embeds it over HTTP (hx-get), not through a Go
	// import.
	rec := c.get("/me")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `id="profile-section"`)
	wantContains(t, rec, `hx-get="/notes"`)

	// An htmx GET /notes answers with the bare fragment that the dashboard
	// swaps in.
	rec = c.do(http.MethodGet, "/notes", nil, true)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `id="notes-section"`)
	if strings.Contains(rec.Body.String(), "<html") {
		t.Fatal("htmx response contains a full page, want a fragment")
	}

	// The same URL stays a full page for plain browsers (the no-JS path).
	rec = c.get("/notes")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "<html")
}

func TestNoteAddValidation(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	// The textarea carries `required`, but the server must not trust the
	// client. Blank text must fail on the htmx path with a 422 fragment.
	rec := c.do(http.MethodPost, "/notes", url.Values{
		"text": {"   "},
	}, true)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "text must not be empty")

	// The no-JS path returns a full page with the error.
	rec = c.postForm("/notes", url.Values{"text": {""}})
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "text must not be empty")
	wantContains(t, rec, "<html")
}

func TestCrossOriginPostRejected(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	// The server must reject a cross-site browser request (the browser sets
	// Sec-Fetch-Site), even with a valid session cookie. This is the CSRF
	// protection.
	req := httptest.NewRequest(http.MethodPost, "/me",
		strings.NewReader(url.Values{"email": {"evil@example.com"}, "name": {"Evil"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(c.cookie)

	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	wantStatus(t, rec, http.StatusForbidden)
}

func TestHealthz(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	rec := c.get("/healthz")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "ok")
}

func TestStaticAssets(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	for _, path := range []string{"/static/htmx.min.js", "/static/style.css"} {
		rec := c.get(path)
		wantStatus(t, rec, http.StatusOK)
		if rec.Body.Len() == 0 {
			t.Errorf("%s: empty body", path)
		}
	}
}
