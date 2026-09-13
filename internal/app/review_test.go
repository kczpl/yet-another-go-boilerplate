package app_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

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
	wantStatus(t, c.get("/healthz"), http.StatusServiceUnavailable)
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
