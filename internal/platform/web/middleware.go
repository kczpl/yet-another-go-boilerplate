package web

import (
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/logging"
)

// LogRequests logs one line per request and propagates X-Request-ID. It
// reuses an incoming id of at most 64 bytes or creates one. It echoes the id
// in the response header and adds it to the log context. Later log records
// repeat this attribute.
func LogRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" || len(requestID) > 64 {
			requestID = rand.Text()
		}
		w.Header().Set("X-Request-ID", requestID)
		ctx := logging.WithAttrs(r.Context(), slog.String("request_id", requestID))
		r = r.WithContext(ctx)

		// Do not log health checks. They drown out real traffic.
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		logger.InfoContext(r.Context(), "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start).Round(time.Microsecond).String(),
		)
	})
}

// SecureHeaders sets the security response headers on every response. The
// CSP permits only same-origin content plus data: images (the favicon);
// htmx and the stylesheet come from /static, so 'self' covers them. Widen
// a directive only with a comment that says why.
func SecureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; form-action 'self'; base-uri 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// RecoverPanics returns a unified 500 before the response starts. It
// aborts the connection if the handler already wrote part of a response.
func RecoverPanics(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer recoverResponse(logger, recorder, r)
		next.ServeHTTP(recorder, r)
	})
}

func recoverResponse(logger *slog.Logger, w *statusRecorder, r *http.Request) {
	cause := recover()
	if cause == nil {
		return
	}
	if cause == http.ErrAbortHandler {
		panic(cause)
	}
	if w.wroteHeader {
		logger.ErrorContext(r.Context(), "panic after response", "error", cause)
		panic(http.ErrAbortHandler)
	}
	RespondError(logger, w, r, fmt.Errorf("handler panic: %v", cause))
}

// statusRecorder captures the status code for the request log.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	if status >= 100 && status < 200 {
		r.ResponseWriter.WriteHeader(status)
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(p)
}

// FlushError forwards a flush and records the implicit status.
func (r *statusRecorder) FlushError() error {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(r.ResponseWriter).Flush()
}

// PageHeaders prevents caches from storing private pages and fragments.
func PageHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Add("Vary", "HX-Request")
		w.Header().Add("Vary", "Accept")
		next.ServeHTTP(w, r)
	})
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
