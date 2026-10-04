package app_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// These tests pin the security invariants of security.md. Extend them.
// Never delete them to make a change pass.

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

func TestCSRFOriginFallback(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	for _, tc := range []struct {
		origin string
		status int
	}{
		{"http://example.com", http.StatusSeeOther},
		{"http://evil.example", http.StatusForbidden},
		{"null", http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodPost, "http://example.com/logout", nil)
		req.Header.Set("Origin", tc.origin)
		rec := httptest.NewRecorder()
		c.handler.ServeHTTP(rec, req)
		wantStatus(t, rec, tc.status)
	}
}

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	want := map[string]string{
		"Content-Security-Policy":   "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; form-action 'self'; base-uri 'self'",
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Referrer-Policy":           "same-origin",
		"Strict-Transport-Security": "max-age=31536000",
	}
	// Pages, static assets, and error pages all get the headers.
	for _, path := range []string{"/login", "/static/style.css", "/no-such-page"} {
		rec := c.get(path)
		for name, wantValue := range want {
			if got := rec.Header().Get(name); got != wantValue {
				t.Errorf("%s: %s = %q, want %q", path, name, got, wantValue)
			}
		}
	}

	// Asset URLs have no version. Browsers must check them again.
	rec := c.get("/static/style.css")
	wantStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Cache-Control"); got != "public, no-cache" {
		t.Errorf("Cache-Control = %q, want %q", got, "public, no-cache")
	}
}

func TestPrivateResponsesDisableCaching(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("cache@example.com", "Cache", "s3cret-pass")
	for _, htmx := range []bool{false, true} {
		for _, path := range []string{"/me", "/notes", "/not-found"} {
			rec := c.do(http.MethodGet, path, nil, htmx)
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("%s: private response can be cached", path)
			}
			vary := strings.Join(rec.Header().Values("Vary"), ", ")
			if !strings.Contains(vary, "HX-Request") || !strings.Contains(vary, "Accept") {
				t.Errorf("%s: Vary = %q", path, vary)
			}
		}
	}
}

func TestRequestBodyLimit(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	// A 2 MiB body exceeds the 1 MiB cap. ParseForm fails in the handler,
	// and the client gets a 400, not a 500.
	rec := c.postForm("/notes", url.Values{"text": {strings.Repeat("a", 2<<20)}})
	wantStatus(t, rec, http.StatusBadRequest)
}

func TestInvalidTextReturnsValidationError(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("text@example.com", "Text", "s3cret-pass")
	for _, text := range []string{"nul\x00", "utf8\xff"} {
		for _, htmx := range []bool{false, true} {
			wantStatus(t, c.do(http.MethodPost, "/notes", url.Values{"text": {text}}, htmx), http.StatusUnprocessableEntity)
			wantStatus(t, c.do(http.MethodPost, "/me", url.Values{"email": {"text@example.com"}, "name": {text}}, htmx), http.StatusUnprocessableEntity)
		}
	}
}

func TestNotesEscapeHTML(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("escape@example.com", "Escape", "s3cret-pass")
	rec := c.do(http.MethodPost, "/notes", url.Values{"text": {`<script>alert("x")</script>`}}, true)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "&lt;script&gt;")
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Fatal("note contains executable HTML")
	}
}
