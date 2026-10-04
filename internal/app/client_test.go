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
	pool := testdb.New(t)
	cfg := config.Load(func(string) string { return "" })
	logger := logging.New(io.Discard, cfg.LogLevel, logging.JSON)
	return &client{
		t:       t,
		pool:    pool,
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

func wantLocation(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}
