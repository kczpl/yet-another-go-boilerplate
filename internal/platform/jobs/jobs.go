// Package jobs is a background job queue on PostgreSQL. A feature enqueues
// a job during a request or in its own transaction. The worker process
// (api worker) claims the due jobs, runs their handlers, and retries a
// failed job after a backoff. A schedule enqueues a job once in each
// interval.
//
// Many worker replicas can share one database. FOR UPDATE SKIP LOCKED
// gives each job to one worker. A lease returns the job of a crashed
// worker to the queue. A schedule row lets only one replica enqueue each
// slot.
//
// Delivery is at least once: a crash or an expired lease runs a job again.
// Write each handler so that a second run is safe.
package jobs

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	// defaultMaxAttempts applies when Params.MaxAttempts is zero.
	defaultMaxAttempts = 10
	// maxKindLength mirrors the CHECK on jobs.kind.
	maxKindLength = 100
)

// Params describes a new job.
type Params struct {
	// Kind selects the handler, for example "auth.delete_expired_sessions".
	// It has 1 to 100 characters: a-z, 0-9, "_", and ".".
	Kind string
	// Payload is the input of the handler. Enqueue encodes it as JSON. A
	// nil value, also a typed nil, means {}. Store ids in the payload, not
	// secrets or private text.
	Payload any
	// RunAt is the earliest start time. The zero time means now.
	RunAt time.Time
	// MaxAttempts caps the runs of the job. Zero means 10.
	MaxAttempts int
}

// DB runs one query. Both *pgxpool.Pool and pgx.Tx satisfy it, so a
// feature can enqueue a job in the transaction that writes its rows. The
// job then exists only when the transaction commits.
type DB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Job is a claimed job.
type Job struct {
	ID      string
	Kind    string
	Payload json.RawMessage
	// Attempt counts the runs of the job. The first run is 1.
	Attempt int

	maxAttempts int
}

// Handler runs one job. A nil error marks the job done. Any other error
// retries the job after a backoff, until the job has no attempts left.
// Wrap an error with Permanent to stop the retries. The handler must
// return when ctx ends.
type Handler func(ctx context.Context, job Job) error

// Enqueue adds a job and returns its id.
func Enqueue(ctx context.Context, db DB, p Params) (string, error) {
	if !validKind(p.Kind) {
		return "", fmt.Errorf("jobs: invalid kind %q", p.Kind)
	}
	if p.MaxAttempts < 0 {
		return "", fmt.Errorf("jobs: negative max attempts %d", p.MaxAttempts)
	}
	payload, err := json.Marshal(p.Payload)
	if err != nil {
		return "", fmt.Errorf("jobs: encoding the payload of %s: %w", p.Kind, err)
	}
	// A nil payload encodes as null. A typed nil, such as a nil map, also
	// encodes as null. Both become {}.
	if string(payload) == "null" {
		payload = []byte("{}")
	}
	id, err := insertJob(ctx, db, p.Kind, payload, p.RunAt, cmp.Or(p.MaxAttempts, defaultMaxAttempts))
	if err != nil {
		return "", fmt.Errorf("enqueueing %s: %w", p.Kind, err)
	}
	return id, nil
}

// Permanent marks err as final: the worker does not retry the job. Use it
// for a failure that a retry cannot fix, for example a bad payload.
// Permanent(nil) returns nil.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

type permanentError struct {
	err error
}

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

func isPermanent(err error) bool {
	var p *permanentError
	return errors.As(err, &p)
}

// validKind mirrors the CHECK on jobs.kind.
func validKind(kind string) bool {
	if kind == "" || len(kind) > maxKindLength {
		return false
	}
	for _, c := range kind {
		if !('a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}
