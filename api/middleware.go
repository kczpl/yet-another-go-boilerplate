package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"
)

type requestIDKey struct{}

// RequestIDFromContext returns the id stored by logRequests, or "" if none.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// logRequests logs one line per request and tags the response with a request
// id (propagating an incoming X-Request-ID). The id is also stored on the
// request context for error logs and handlers. /healthz is skipped to keep
// probe noise out of the logs.
func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = newRequestID()
		}
		w.Header().Set("X-Request-ID", requestID)
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, requestID))

		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)

		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", requestID,
		)
	})
}

// recoverPanics converts a panicking handler into a 500 so one bad request
// cannot kill the process. It is a last resort, not a control-flow mechanism.
func recoverPanics(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				logger.Error("panic recovered",
					"panic", v,
					"method", r.Method,
					"path", r.URL.Path,
					"request_id", RequestIDFromContext(r.Context()),
				)
				respondError(w, http.StatusInternalServerError, "internal server error", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the status code for the request log.
// Unwrap lets http.ResponseController and similar helpers reach the
// underlying writer (flush, deadlines, hijack).
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return hex.EncodeToString(b)
}
