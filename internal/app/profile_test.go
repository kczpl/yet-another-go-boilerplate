package app_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

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
	wantLocation(t, rec, "/me?saved=1")
	rec = c.get("/me?saved=1")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Profile saved.")
	wantContains(t, rec, "Bob Again")
}
