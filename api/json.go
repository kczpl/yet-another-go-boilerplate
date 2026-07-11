package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

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
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		return v, fmt.Errorf("decoding json: %w", err)
	}
	return v, nil
}

// decodeValid decodes the request body and validates it. A non-nil problems
// map means the body parsed but failed validation (respond 422); an error
// with nil problems means the body did not parse (respond 400).
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
