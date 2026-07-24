package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Cap on JSON request bodies. Oversized payloads answer 413.
const maxJSONBodyBytes = 1 << 20 // 1 MiB

// Validator is implemented by request types that validate themselves. Valid
// returns field name → problem description; an empty map means valid.
type Validator interface {
	Valid(ctx context.Context) (problems map[string]string)
}

func encode[T any](w http.ResponseWriter, status int, v T) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// An encode error here means the client went away mid-response; there is
	// nothing left to send them.
	_ = json.NewEncoder(w).Encode(v)
}

func decode[T any](r *http.Request) (T, error) {
	var v T
	// nil ResponseWriter: still enforces the limit; we map MaxBytesError to 413.
	body := http.MaxBytesReader(nil, r.Body, maxJSONBodyBytes)
	if err := json.NewDecoder(body).Decode(&v); err != nil {
		return v, fmt.Errorf("decoding json: %w", err)
	}
	return v, nil
}

// decodeValid decodes the request body and validates it. A non-nil problems
// map means the body parsed but failed validation (respond 422); an error
// with nil problems means the body did not parse (respond 400) or exceeded
// maxJSONBodyBytes (respond 413).
func decodeValid[T Validator](r *http.Request) (T, map[string]string, error) {
	v, err := decode[T](r)
	if err != nil {
		return v, nil, err
	}
	if problems := v.Valid(r.Context()); len(problems) > 0 {
		return v, problems, fmt.Errorf("invalid %T: %d problems", v, len(problems))
	}
	return v, nil, nil
}

// respondBodyError maps body decode failures: 413 when the payload exceeds
// maxJSONBodyBytes, otherwise 400 for malformed JSON.
func respondBodyError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		respondError(w, http.StatusRequestEntityTooLarge, "request body too large", nil)
		return
	}
	respondError(w, http.StatusBadRequest, "invalid json body", nil)
}
