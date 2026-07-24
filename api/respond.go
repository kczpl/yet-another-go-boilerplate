package api

import (
	"log/slog"
	"net/http"
)

// Response envelopes, defined once so every endpoint answers the same shape:
// success {"data": ...}, lists {"data": [...], "pagination": {...}}, errors
// {"error": "...", "problems": {...}}.

type dataResponse[T any] struct {
	Data T `json:"data"`
}

type listResponse[T any] struct {
	Data       []T        `json:"data"`
	Pagination pagination `json:"pagination"`
}

type errorResponse struct {
	Error    string            `json:"error"`
	Problems map[string]string `json:"problems,omitempty"`
}

func respondData[T any](w http.ResponseWriter, status int, data T) {
	encode(w, status, dataResponse[T]{Data: data})
}

func respondError(w http.ResponseWriter, status int, message string, problems map[string]string) {
	encode(w, status, errorResponse{Error: message, Problems: problems})
}

// respondInternalError logs the real error with request context and returns
// an opaque 500 — internals never leak to clients, and errors are logged
// exactly once, here at the boundary.
func respondInternalError(w http.ResponseWriter, logger *slog.Logger, r *http.Request, err error) {
	logger.Error("request failed",
		"method", r.Method,
		"path", r.URL.Path,
		"request_id", RequestIDFromContext(r.Context()),
		"err", err,
	)
	respondError(w, http.StatusInternalServerError, "internal server error", nil)
}
