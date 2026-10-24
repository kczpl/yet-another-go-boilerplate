package web_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/logging"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/web"
)

func TestWantsEventStream(t *testing.T) {
	t.Parallel()
	tests := []struct {
		accept string
		want   bool
	}{
		{"", false},
		{"text/html,application/xhtml+xml,*/*;q=0.8", false},
		{"application/json", false},
		{"text/event-stream", true},
		{"text/event-stream, */*", true},
	}
	for _, tt := range tests {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.Header.Set("Accept", tt.accept)
		if got := web.WantsEventStream(r); got != tt.want {
			t.Errorf("WantsEventStream(Accept: %q) = %v, want %v", tt.accept, got, tt.want)
		}
	}
}

func TestEventStreamWritesEvents(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	stream, err := web.NewEventStream(rec)
	if err != nil {
		t.Fatalf("NewEventStream: %v", err)
	}
	if !rec.Flushed {
		t.Error("the stream headers were not flushed")
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}

	if err := stream.Send("delta", "one\r\ntwo\rthree\n"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := stream.Send("", " leading space"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	want := "event: delta\ndata: one\ndata: two\ndata: three\ndata: \n\n" +
		"data:  leading space\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestEventStreamRejectsLineBreakInEventName(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	stream, err := web.NewEventStream(rec)
	if err != nil {
		t.Fatalf("NewEventStream: %v", err)
	}
	if err := stream.Send("delta\ndata: injected", "x"); err == nil {
		t.Fatal("Send accepted an event name with a line break")
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want nothing", rec.Body.String())
	}
}

// TestEventStreamOutlivesServerTimeouts runs a real server, because only
// a real connection has read and write deadlines. The handler sends one
// event before and one event after both timeouts.
func TestEventStreamOutlivesServerTimeouts(t *testing.T) {
	t.Parallel()
	const timeout = 50 * time.Millisecond
	handlerErr := make(chan error, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerErr <- streamPastTimeout(w, r, 3*timeout)
	}))
	srv.Config.ReadTimeout = timeout
	srv.Config.WriteTimeout = timeout
	srv.Start()
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the stream: %v", err)
	}
	if err := <-handlerErr; err != nil {
		t.Fatalf("handler: %v", err)
	}
	if want := "data: first\n\ndata: second\n\n"; string(body) != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

// streamPastTimeout sends one event, waits longer than the server
// timeouts, and sends a second event. The request context must not end
// while it waits: the server removes the read deadline when it starts its
// background read.
func streamPastTimeout(w http.ResponseWriter, r *http.Request, wait time.Duration) error {
	stream, err := web.NewEventStream(w)
	if err != nil {
		return err
	}
	if err := stream.Send("", "first"); err != nil {
		return err
	}
	select {
	case <-time.After(wait):
	case <-r.Context().Done():
		return fmt.Errorf("the request context ended: %w", r.Context().Err())
	}
	return stream.Send("", "second")
}

func TestErrorAfterStreamStartOnlyLogs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		clientLeft bool
		wantLevel  string
	}{
		{"server fault", false, `"level":"ERROR"`},
		{"client left", true, `"level":"INFO"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var logs strings.Builder
			logger := logging.New(&logs, slog.LevelInfo, logging.JSON)
			h := web.E(logger, func(w http.ResponseWriter, _ *http.Request) error {
				stream, err := web.NewEventStream(w)
				if err != nil {
					return err
				}
				_ = stream.Send("delta", "partial")
				return errors.New("private cause")
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.clientLeft {
				cancel()
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodPost, "/", nil))

			if want := "event: delta\ndata: partial\n\n"; rec.Body.String() != want {
				t.Errorf("body = %q, want %q: an error after the start must not add an error page", rec.Body.String(), want)
			}
			if !strings.Contains(logs.String(), tt.wantLevel) || !strings.Contains(logs.String(), "private cause") {
				t.Errorf("log = %s, want %s with the cause", logs.String(), tt.wantLevel)
			}
		})
	}
}
