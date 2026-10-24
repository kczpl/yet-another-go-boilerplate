package app_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kczpl/yet-another-go-boilerplate/internal/app"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/llm"
)

// ask posts a question and asks for an event stream, like static/app.js.
func (c *client) ask(question string) *httptest.ResponseRecorder {
	c.t.Helper()
	req := newRequest(http.MethodPost, "/chat", url.Values{"question": {question}})
	req.Header.Set("Accept", "text/event-stream")
	return c.send(req)
}

func TestChatRequiresLogin(t *testing.T) {
	t.Parallel()
	c := newClient(t)

	rec := c.get("/chat")
	wantStatus(t, rec, http.StatusSeeOther)
	wantLocation(t, rec, "/login")

	// The model must not run for an anonymous client.
	rec = c.ask("hello")
	wantStatus(t, rec, http.StatusSeeOther)
	wantLocation(t, rec, "/login")
}

func TestChatStreamsAnswer(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	rec := c.ask("hello streaming world")
	wantStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	// The fake model echoes the question one word at a time.
	wantBody(t, rec, "event: delta\ndata: hello \n\n"+
		"event: delta\ndata: streaming \n\n"+
		"event: delta\ndata: world\n\n"+
		"event: done\ndata: end\n\n")
}

func TestChatStreamRejectsEmptyQuestion(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	// The stream starts before the check, so the error is an event.
	rec := c.ask("   ")
	wantStatus(t, rec, http.StatusOK)
	wantBody(t, rec, "event: error\ndata: question must not be empty\n\n")
}

func TestChatNoJS(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	rec := c.get("/chat")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `data-stream="#answer"`)

	// A plain form post gets the full page with the complete answer. The
	// template escapes the model text.
	rec = c.postForm("/chat", url.Values{"question": {"<b>hi</b> there"}})
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `<div id="answer" class="answer" aria-live="polite">&lt;b&gt;hi&lt;/b&gt; there</div>`)

	rec = c.postForm("/chat", url.Values{"question": {""}})
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "question must not be empty")
	wantPage(t, rec)
}

// failingModel sends one part of an answer and then fails.
type failingModel struct{}

func (failingModel) Stream(_ context.Context, _ llm.Request, onText func(string) error) (llm.Response, error) {
	if err := onText("partial"); err != nil {
		return llm.Response{}, err
	}
	return llm.Response{}, errors.New("private cause: upstream 529")
}

func TestChatModelFailure(t *testing.T) {
	t.Parallel()
	c := newClientWithModels(t, app.Models{LLM: failingModel{}})
	c.register("bob@example.com", "Bob", "s3cret-pass")

	// The status is already 200, so the failure arrives as an event with a
	// generic message. The cause goes only to the log.
	rec := c.ask("hello")
	wantStatus(t, rec, http.StatusOK)
	wantBody(t, rec, "event: delta\ndata: partial\n\n"+
		"event: error\ndata: the answer failed, try again\n\n")
	if strings.Contains(rec.Body.String(), "private cause") {
		t.Fatal("stream leaks the internal error")
	}

	// Without JavaScript, nothing was sent yet, so the client gets the
	// opaque 500 page.
	rec = c.postForm("/chat", url.Values{"question": {"hello"}})
	wantStatus(t, rec, http.StatusInternalServerError)
	wantPage(t, rec)
	if strings.Contains(rec.Body.String(), "private cause") || strings.Contains(rec.Body.String(), "partial") {
		t.Fatal("the error page leaks the internal error or the partial answer")
	}
}

// slowModel answers only after wait.
type slowModel struct {
	wait time.Duration
}

func (m slowModel) Stream(ctx context.Context, _ llm.Request, onText func(string) error) (llm.Response, error) {
	select {
	case <-ctx.Done():
		return llm.Response{}, ctx.Err()
	case <-time.After(m.wait):
	}
	if err := onText("late"); err != nil {
		return llm.Response{}, err
	}
	return llm.Response{Text: "late", StopReason: llm.StopEnd}, nil
}

// TestChatOutlivesWriteTimeout runs the full handler on a real server,
// because only a real connection has a write deadline. The model answers
// after http.Server.WriteTimeout. Both response kinds must still reach the
// client. A middleware that hides http.ResponseController from the
// handler also fails this test.
func TestChatOutlivesWriteTimeout(t *testing.T) {
	t.Parallel()
	const timeout = 50 * time.Millisecond
	c := newClientWithModels(t, app.Models{LLM: slowModel{wait: 3 * timeout}})
	c.register("bob@example.com", "Bob", "s3cret-pass")
	srv := httptest.NewUnstartedServer(c.handler)
	srv.Config.WriteTimeout = timeout
	srv.Start()
	t.Cleanup(srv.Close)

	tests := []struct {
		name   string
		accept string
		want   string
	}{
		{"stream", "text/event-stream", "event: delta\ndata: late\n\n"},
		{"no js", "text/html", `aria-live="polite">late</div>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{"question": {"hello"}}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/chat", strings.NewReader(form.Encode()))
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Accept", tt.accept)
			req.AddCookie(c.cookie)

			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatalf("POST /chat: %v", err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("reading the answer: %v", err)
			}
			if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), tt.want) {
				t.Fatalf("status = %d, body = %q, want 200 with %q", resp.StatusCode, body, tt.want)
			}
		})
	}
}

func TestChatRateLimit(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	c.register("bob@example.com", "Bob", "s3cret-pass")

	for i := range 5 {
		if rec := c.ask("hello"); rec.Code != http.StatusOK {
			t.Fatalf("question %d: status = %d, want 200", i+1, rec.Code)
		}
	}
	// The limit applies before the stream starts, so the client gets a
	// real status. static/app.js then posts the form without JavaScript
	// and shows the error page.
	rec := c.ask("hello")
	wantStatus(t, rec, http.StatusTooManyRequests)
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 without Retry-After")
	}
}
