package web

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

//go:embed error.html
var errorFS embed.FS

// errorTmpl renders the shared error page inside the layout.
var errorTmpl = MustPage(errorFS, "error.html")

// HTTPError is an error with an HTTP status. Msg is safe to send to the
// client. Err is the internal cause; it goes only to the log.
type HTTPError struct {
	Status int
	Msg    string
	Err    error
}

func (e *HTTPError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}

func (e *HTTPError) Unwrap() error { return e.Err }

// BadRequest builds a 400 with a client-safe message.
func BadRequest(msg string) *HTTPError {
	return &HTTPError{Status: http.StatusBadRequest, Msg: msg}
}

// Unauthorized builds a 401 with a client-safe message.
func Unauthorized(msg string) *HTTPError {
	return &HTTPError{Status: http.StatusUnauthorized, Msg: msg}
}

// NotFound builds a 404 with a client-safe message.
func NotFound(msg string) *HTTPError {
	return &HTTPError{Status: http.StatusNotFound, Msg: msg}
}

// HandlerE is a handler that returns its error instead of writing the error
// response itself. Wrap it with E.
type HandlerE func(w http.ResponseWriter, r *http.Request) error

// E adapts a HandlerE to http.Handler. A returned error goes through
// RespondError, so every endpoint fails in the same format.
func E(logger *slog.Logger, h HandlerE) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			RespondError(logger, w, r, err)
		}
	})
}

// RespondError writes the unified error response. An *HTTPError keeps its
// status and message; every other error becomes an opaque 500. JSON clients
// (Accept: application/json) get a JSON envelope. Everyone else gets the
// shared error page. Only Msg reaches the client.
func RespondError(logger *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		httpErr = &HTTPError{Status: http.StatusInternalServerError, Err: err}
	}
	msg := httpErr.Msg
	if msg == "" {
		msg = strings.ToLower(http.StatusText(httpErr.Status))
	}

	// A plain 4xx is normal traffic, and the request log already shows its
	// status. Log only server faults and errors with an internal cause.
	if httpErr.Status >= 500 || httpErr.Err != nil {
		logger.ErrorContext(r.Context(), "request error",
			"method", r.Method,
			"path", r.URL.Path,
			"status", httpErr.Status,
			"error", err,
		)
	}

	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(httpErr.Status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"status": httpErr.Status, "message": msg},
		})
		return
	}
	RenderPage(r.Context(), logger, w, httpErr.Status, errorTmpl, errorData{
		Page:       Page{Title: http.StatusText(httpErr.Status)},
		Status:     httpErr.Status,
		StatusText: http.StatusText(httpErr.Status),
		Msg:        msg,
	})
}

type errorData struct {
	Page
	Status     int
	StatusText string
	Msg        string
}
