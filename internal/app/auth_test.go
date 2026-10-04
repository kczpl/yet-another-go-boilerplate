package app_test

import (
	"net/http"
	"net/url"
	"testing"
)

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
	wantLocation(t, rec, "/login")

	// Log back in. The uppercase email must match: login is
	// case-insensitive.
	rec = c.postForm("/login", url.Values{
		"email": {"BOB@example.com"}, "password": {"s3cret-pass"},
	})
	wantStatus(t, rec, http.StatusSeeOther)
	rec = c.get("/me")
	wantStatus(t, rec, http.StatusOK)
}

func TestSessionCookieFlags(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	// The __Host- prefix makes the browser reject the cookie unless it is
	// Secure, has Path=/, and has no Domain.
	ck := c.cookie
	if ck.Name != sessionCookie || !ck.Secure || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" || ck.Domain != "" {
		t.Fatalf("session cookie = %+v, want %s with Secure, HttpOnly, SameSite=Lax, Path=/, and no Domain", ck, sessionCookie)
	}
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

func TestLoginRateLimit(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	// The burst is 10 attempts (loginBurst in app.go). A malformed email
	// fails before the password hash, so the test stays fast.
	bad := url.Values{"email": {"nobody"}, "password": {"guess"}}
	for i := range 10 {
		rec := c.postForm("/login", bad)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("attempt %d was limited inside the burst", i+1)
		}
	}
	rec := c.postForm("/login", bad)
	wantStatus(t, rec, http.StatusTooManyRequests)
	wantContains(t, rec, "too many attempts")
	if got := rec.Header().Get("Retry-After"); got != "20" {
		t.Errorf("Retry-After = %q, want 20 (loginInterval)", got)
	}

	// The limit applies only to POST /login. The login page still opens.
	wantStatus(t, c.get("/login"), http.StatusOK)
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

func TestRequireIdentity(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	// A browser request gets a redirect to the login page.
	rec := c.get("/me")
	wantStatus(t, rec, http.StatusSeeOther)
	wantLocation(t, rec, "/login")

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
	wantLocation(t, rec, "/login")

	c.register("bob@example.com", "Bob", "s3cret-pass")
	rec = c.get("/")
	wantStatus(t, rec, http.StatusSeeOther)
	wantLocation(t, rec, "/me")
}

func TestSessionDatabaseFailureReturnsServerError(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("failure@example.com", "Failure", "s3cret-pass")
	c.pool.Close()
	for _, htmx := range []bool{false, true} {
		rec := c.do(http.MethodGet, "/me", nil, htmx)
		wantStatus(t, rec, http.StatusInternalServerError)
		if rec.Header().Get("Location") != "" || rec.Header().Get("HX-Redirect") != "" {
			t.Fatal("database failure redirected to login")
		}
		if c.cookie == nil {
			t.Fatal("database failure cleared the session cookie")
		}
	}
	wantStatus(t, c.get("/static/htmx.min.js"), http.StatusOK)
}
