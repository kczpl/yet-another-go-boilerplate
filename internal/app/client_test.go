package app_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kczpl/yet-another-go-boilerplate/internal/app"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/config"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/logging"
	"github.com/kczpl/yet-another-go-boilerplate/internal/testdb"
	"github.com/kczpl/yet-another-go-boilerplate/internal/user"
)

// sessionCookie is the cookie name outside development.
const sessionCookie = "__Host-session_id"

// client drives the full production handler (app.New: middleware, auth,
// routes, templates) through httptest. It carries the session cookie between
// requests, like a browser.
type client struct {
	t       *testing.T
	handler http.Handler
	pool    *pgxpool.Pool
	users   *user.Service
	cookie  *http.Cookie
}

// newClient builds the handler with the production configuration: an
// unset ENVIRONMENT means production. The tests then cover the secure
// cookie and HSTS. The pool of the test replaces DATABASE_URL.
func newClient(t *testing.T) *client {
	t.Helper()
	return newClientWithModels(t, app.Models{})
}

// newClientWithModels is newClient with test providers. A nil field of
// stubs keeps the configured model. A test provider gets the configured
// model name ("fake/echo" or "fake/uniform") unchanged.
func newClientWithModels(t *testing.T, stubs app.Models) *client {
	t.Helper()
	pool := testdb.New(t)
	cfg := config.Load(func(string) string { return "" })
	models, err := app.NewModels(cfg)
	if err != nil {
		t.Fatalf("NewModels: %v", err)
	}
	if stubs.LLM != nil {
		models.LLM = stubs.LLM
	}
	if stubs.SystemOne != nil {
		models.SystemOne = stubs.SystemOne
	}
	logger := logging.New(io.Discard, cfg.LogLevel, logging.JSON)
	return &client{
		t:       t,
		pool:    pool,
		handler: app.New(logger, cfg, pool, models),
		users:   user.NewService(user.NewRepo(pool)),
	}
}

// newRequest builds a request. A non-nil form becomes the body.
func newRequest(method, path string, form url.Values) *http.Request {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return req
}

// do sends one request. When htmx is true, the request carries the
// HX-Request header, like a request from htmx in the browser.
func (c *client) do(method, path string, form url.Values, htmx bool) *httptest.ResponseRecorder {
	c.t.Helper()
	req := newRequest(method, path, form)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	return c.send(req)
}

// send runs req through the handler with the session cookie of the client.
// It keeps a new session cookie from the response.
func (c *client) send(req *http.Request) *httptest.ResponseRecorder {
	c.t.Helper()
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}

	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)

	for _, ck := range rec.Result().Cookies() {
		if ck.Name == sessionCookie {
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

func (c *client) htmxGet(path string) *httptest.ResponseRecorder {
	return c.do(http.MethodGet, path, nil, true)
}

func (c *client) htmxPost(path string, form url.Values) *httptest.ResponseRecorder {
	return c.do(http.MethodPost, path, form, true)
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

// wantBody checks the exact body, for example an event stream.
func wantBody(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if got := rec.Body.String(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

// wantPage checks that the body is a full page with the layout.
func wantPage(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	wantContains(t, rec, "<html")
}

// wantFragment checks that the body is a bare fragment for an htmx swap,
// not a full page.
func wantFragment(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("body is a full page, want a fragment:\n%s", rec.Body.String())
	}
}

func wantLocation(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}
