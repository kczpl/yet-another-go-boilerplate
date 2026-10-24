package app_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/kczpl/yet-another-go-boilerplate/internal/app"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/systemone"
)

func TestTriageRequiresLogin(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	rec := c.get("/triage")
	wantStatus(t, rec, http.StatusSeeOther)
	wantLocation(t, rec, "/login")

	// The model must not run for an anonymous client.
	rec = c.postForm("/triage", url.Values{"message": {"help"}})
	wantStatus(t, rec, http.StatusSeeOther)
	wantLocation(t, rec, "/login")
}

func TestTriageWithFakeModel(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	rec := c.get("/triage")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `method="post" action="/triage"`)
	wantContains(t, rec, `hx-post="/triage"`)

	// htmx gets only the section. The fake model knows nothing, so a
	// person must review the message.
	rec = c.htmxPost("/triage", url.Values{"message": {"I was charged twice."}})
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `<section id="triage-section"`)
	wantContains(t, rec, "<dd>review: the model is not sure, so a person picks the queue</dd>")
	wantContains(t, rec, "<dd>no (probability 0.50)</dd>")
	wantFragment(t, rec)

	// A plain form post gets the full page with the decision. The
	// template escapes the message.
	rec = c.postForm("/triage", url.Values{"message": {"<b>refund</b> please"}})
	wantStatus(t, rec, http.StatusOK)
	wantPage(t, rec)
	wantContains(t, rec, "&lt;b&gt;refund&lt;/b&gt; please</textarea>")
	wantContains(t, rec, "<dd>uniform</dd>")
}

// sureModel is sure that each message is an urgent bug.
type sureModel struct{}

func (sureModel) Ask(context.Context, systemone.Request) (systemone.Response, error) {
	return systemone.Response{
		Model: "sure-1.0",
		Answers: map[string]systemone.Answer{
			"topic":  {Type: systemone.Choice, Choice: "bug", Confidence: 0.9},
			"urgent": {Type: systemone.Noul, Noul: 0.95},
		},
	}, nil
}

func TestTriageSureDecision(t *testing.T) {
	t.Parallel()
	c := newClientWithModels(t, app.Models{SystemOne: sureModel{}})
	c.register("bob@example.com", "Bob", "s3cret-pass")

	rec := c.htmxPost("/triage", url.Values{"message": {"The app crashes."}})
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "<dd>bug</dd>")
	wantContains(t, rec, "<dd>0.90</dd>")
	wantContains(t, rec, "<dd>yes (probability 0.95)</dd>")
	wantContains(t, rec, "<dd>sure-1.0</dd>")
}

func TestTriageValidation(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	// htmx gets the section with the error. The htmx config swaps a 422.
	rec := c.htmxPost("/triage", url.Values{"message": {"  "}})
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, `<section id="triage-section"`)
	wantContains(t, rec, "message must not be empty")

	// A plain form post gets the full page and keeps the message.
	long := strings.Repeat("a", 4001)
	rec = c.postForm("/triage", url.Values{"message": {long}})
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantPage(t, rec)
	wantContains(t, rec, "message must be at most 4000 characters")
	wantContains(t, rec, long+"</textarea>")
}

// brokenModel fails with an internal cause.
type brokenModel struct{}

func (brokenModel) Ask(context.Context, systemone.Request) (systemone.Response, error) {
	return systemone.Response{}, errors.New("private cause: upstream 529")
}

// strangeModel answers with a queue that the feature does not know.
type strangeModel struct{}

func (strangeModel) Ask(context.Context, systemone.Request) (systemone.Response, error) {
	return systemone.Response{
		Model: "strange-1.0",
		Answers: map[string]systemone.Answer{
			"topic":  {Type: systemone.Choice, Choice: "private-label", Confidence: 0.9},
			"urgent": {Type: systemone.Noul, Noul: 0.1},
		},
	}, nil
}

func TestTriageModelFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		model systemone.Provider
	}{
		{"model error", brokenModel{}},
		{"invalid answer", strangeModel{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newClientWithModels(t, app.Models{SystemOne: tt.model})
			c.register("bob@example.com", "Bob", "s3cret-pass")

			// Both paths get the opaque 500 page. The cause goes only to
			// the log.
			for _, htmx := range []bool{true, false} {
				rec := c.do(http.MethodPost, "/triage", url.Values{"message": {"help"}}, htmx)
				wantStatus(t, rec, http.StatusInternalServerError)
				if body := rec.Body.String(); strings.Contains(body, "private") {
					t.Fatalf("htmx=%v: the error page leaks the internal error:\n%s", htmx, body)
				}
			}
		})
	}
}

func TestTriageRateLimit(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	for i := range 10 {
		if rec := c.postForm("/triage", url.Values{"message": {"help"}}); rec.Code != http.StatusOK {
			t.Fatalf("triage %d: status = %d, want 200", i+1, rec.Code)
		}
	}
	rec := c.postForm("/triage", url.Values{"message": {"help"}})
	wantStatus(t, rec, http.StatusTooManyRequests)
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 without Retry-After")
	}
}
