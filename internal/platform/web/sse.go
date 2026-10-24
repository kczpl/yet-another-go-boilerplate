package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// WantsEventStream reports whether the client asked for an event stream
// (Accept: text/event-stream). A handler uses it to choose a stream or a
// complete page, so the flow also works without JavaScript.
func WantsEventStream(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}

// RemoveWriteDeadline removes the http.Server.WriteTimeout deadline from
// this response. Call it before work that can last longer than the
// timeout, for example a model call. When the deadline passes, the server
// drops the response, but the request context does not end, so the work
// runs to its end for nothing. A test recorder has no deadline; the call
// then does nothing.
func RemoveWriteDeadline(w http.ResponseWriter) error {
	err := http.NewResponseController(w).SetWriteDeadline(time.Time{})
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		return fmt.Errorf("removing write deadline: %w", err)
	}
	return nil
}

// EventStream writes Server-Sent Events (text/event-stream) to one
// response. The client reads the events with fetch or EventSource.
type EventStream struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

// NewEventStream starts an event stream on w. It sends the 200 status and
// the stream headers at once, so the client knows that the stream is open.
// After this call the handler can no longer send an error status. It must
// tell the client about an error with an event. A stream can last longer
// than http.Server.WriteTimeout, so NewEventStream removes the write
// deadline.
func NewEventStream(w http.ResponseWriter) (*EventStream, error) {
	if err := RemoveWriteDeadline(w); err != nil {
		return nil, err
	}
	rc := http.NewResponseController(w)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	// Tell a buffering proxy (for example nginx) to pass each event on at
	// once.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		return nil, fmt.Errorf("flushing stream headers: %w", err)
	}
	return &EventStream{w: w, rc: rc}, nil
}

// Send writes one event and flushes it to the client. An empty event name
// sends a default "message" event. Each line of data becomes one data
// field, so the client receives the data with its line breaks. Send
// changes CRLF and CR line breaks to LF.
func (s *EventStream) Send(event, data string) error {
	if strings.ContainsAny(event, "\r\n") {
		return fmt.Errorf("event name %q contains a line break", event)
	}
	var b strings.Builder
	if event != "" {
		b.WriteString("event: " + event + "\n")
	}
	data = strings.ReplaceAll(data, "\r\n", "\n")
	data = strings.ReplaceAll(data, "\r", "\n")
	for line := range strings.SplitSeq(data, "\n") {
		b.WriteString("data: " + line + "\n")
	}
	// An empty line ends the event.
	b.WriteString("\n")

	if _, err := io.WriteString(s.w, b.String()); err != nil {
		return fmt.Errorf("writing event: %w", err)
	}
	if err := s.rc.Flush(); err != nil {
		return fmt.Errorf("flushing event: %w", err)
	}
	return nil
}
