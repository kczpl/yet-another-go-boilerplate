package app_test

import (
	"net/http"
	"testing"
)

func TestProbes(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	for _, path := range []string{"/livez", "/readyz"} {
		rec := c.get(path)
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, "ok")
	}

	// Without the database, the process is alive but not ready. A restart
	// does not repair the database, so livez stays 200.
	c.pool.Close()
	rec := c.get("/readyz")
	wantStatus(t, rec, http.StatusServiceUnavailable)
	wantContains(t, rec, "database unreachable")
	wantStatus(t, c.get("/livez"), http.StatusOK)
}

func TestStaticAssets(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	for _, path := range []string{"/static/htmx.min.js", "/static/style.css", "/static/app.js"} {
		rec := c.get(path)
		wantStatus(t, rec, http.StatusOK)
		if rec.Body.Len() == 0 {
			t.Errorf("%s: empty body", path)
		}
	}
}
