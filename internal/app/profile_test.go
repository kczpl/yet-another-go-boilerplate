package app_test

import (
	"net/http"
	"net/url"
	"testing"
)

func TestProfileUpdate(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	// This is the htmx path. The response is the bare fragment, not a full
	// page.
	rec := c.htmxPost("/me", url.Values{
		"email": {"robert@example.com"}, "name": {"Robert"},
	})
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `id="profile-section"`)
	wantContains(t, rec, "Profile saved.")
	wantContains(t, rec, "robert@example.com")
	wantFragment(t, rec)

	// An htmx validation error returns a 422 fragment with the message and
	// the input.
	rec = c.htmxPost("/me", url.Values{
		"email": {"not-an-email"}, "name": {"Robert"},
	})
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

func TestProfileUpdateErrorsWithoutJS(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")
	if _, err := c.users.Register(t.Context(), "alice@example.com", "Alice", "s3cret-pass"); err != nil {
		t.Fatalf("register: %v", err)
	}

	// A plain form post with an error gets the full page with the message
	// and the submitted values. There is no redirect.
	rec := c.postForm("/me", url.Values{
		"email": {"not-an-email"}, "name": {"Robert"},
	})
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantPage(t, rec)
	wantContains(t, rec, "enter a valid email address")
	wantContains(t, rec, `value="not-an-email"`)
	wantContains(t, rec, `value="Robert"`)

	// The email of another account is a conflict, not a 500. The check
	// ignores case.
	rec = c.postForm("/me", url.Values{
		"email": {"ALICE@example.com"}, "name": {"Bob"},
	})
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantPage(t, rec)
	wantContains(t, rec, "email already registered")

	// The failed posts changed nothing.
	rec = c.get("/me")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "<dd>bob@example.com</dd>")
}

// TestDashboardEmbedsFeatures checks the /me dashboard. Each card belongs
// to one feature. The browser loads the card over HTTP (hx-get), so the
// user feature imports no other feature.
func TestDashboardEmbedsFeatures(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	dashboard := c.get("/me")
	wantStatus(t, dashboard, http.StatusOK)
	wantContains(t, dashboard, `id="profile-section"`)

	tests := []struct {
		path, section, detail string
	}{
		{"/notes", "notes-section", "from PostgreSQL"},
		{"/chat", "chat-section", "<code>fake/echo</code>"},
		{"/triage", "triage-section", "<code>fake/uniform</code>"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			// The placeholder loads the section. Without JavaScript, its
			// link leads to the full page.
			wantContains(t, dashboard, `hx-get="`+tt.path+`"`)
			wantContains(t, dashboard, `<a href="`+tt.path+`">`)

			// An htmx GET answers with the bare section that the
			// dashboard swaps in.
			rec := c.htmxGet(tt.path)
			wantStatus(t, rec, http.StatusOK)
			wantContains(t, rec, `<section id="`+tt.section+`"`)
			wantContains(t, rec, tt.detail)
			wantFragment(t, rec)

			// The same URL stays a full page for plain browsers.
			rec = c.get(tt.path)
			wantStatus(t, rec, http.StatusOK)
			wantPage(t, rec)
		})
	}
}
