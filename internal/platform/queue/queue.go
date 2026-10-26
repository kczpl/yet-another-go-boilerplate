// Package queue runs background jobs on PgQue (https://pgque.dev), a
// message queue in PostgreSQL. A migration installs PgQue as plain SQL
// (migrations/*_install_pgque.sql). The package calls the SQL API of
// PgQue with pgx and adds no Go module.
//
// A producer sends a message to a queue with Send, in the transaction
// that writes its rows. A consumer is a named cursor on a queue. A
// migration creates each queue and subscribes each consumer. The worker
// process (api worker) drives the PgQue ticker and the PgQue maintenance,
// receives the batches of each consumer, and runs the handler of each
// message type.
//
// A failed message returns to the queue after a backoff (pgque.nack).
// After the retry limit of its queue, the message goes to the dead letter
// queue (pgque.dead_letter). A schedule sends a message once in each
// interval.
//
// Many worker processes can share one database. One worker is active: it
// holds a session advisory lock. The other workers wait and take over
// when the active worker stops.
//
// Delivery is at least once: a crash or a stop in the middle of a batch
// delivers the whole batch again. Write each handler so that a second run
// is safe.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const (
	// maxNameLength caps a queue name, a consumer name, and a message
	// type. PgQue allows queue names of up to 57 bytes.
	maxNameLength = 57
	// maxErrorBytes caps the error text that the dead letter queue stores.
	maxErrorBytes = 2000
)

// DB runs one query. *pgxpool.Pool, *pgxpool.Conn, and pgx.Tx satisfy it,
// so a feature can send a message in the transaction that writes its
// rows. The message then exists only when the transaction commits.
type DB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Message is one message of a batch.
type Message struct {
	ID   int64
	Type string
	// Payload is the JSON object that Send stored.
	Payload json.RawMessage
	// Retries counts the earlier deliveries that failed. The first
	// delivery has 0.
	Retries int
	// CreatedAt is the time of the first send.
	CreatedAt time.Time
}

// Handler handles one message. A nil error marks the message done. Any
// other error sends the message back to the queue after a backoff, until
// the retry limit of the queue sends it to the dead letter queue. Wrap an
// error with Permanent to send the message to the dead letter queue at
// once. The handler must return when ctx ends.
type Handler func(ctx context.Context, msg Message) error

// Send adds a message of type typ to queue and returns its id. It encodes
// payload as JSON. A nil payload, also a typed nil, means {}. Store ids
// in the payload, not secrets or private text.
//
// PgQue delivers the message after the next tick that follows the commit:
// usually within a few seconds.
func Send(ctx context.Context, db DB, queue, typ string, payload any) (int64, error) {
	if !validName(typ) {
		return 0, fmt.Errorf("queue: invalid message type %q", typ)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("queue: encoding the payload of %s: %w", typ, err)
	}
	// A nil payload encodes as null. A typed nil, such as a nil map, also
	// encodes as null. Both become {}.
	if string(data) == "null" {
		data = []byte("{}")
	}
	id, err := send(ctx, db, queue, typ, data)
	if err != nil {
		return 0, fmt.Errorf("sending %s to %s: %w", typ, queue, err)
	}
	return id, nil
}

// Permanent marks err as final: the worker does not retry the message
// and sends it to the dead letter queue at once. Use it for a failure
// that a retry cannot fix, for example a bad payload. Permanent(nil)
// returns nil.
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

// validName accepts 1 to 57 characters: a-z, 0-9, "_", and ".".
func validName(name string) bool {
	if name == "" || len(name) > maxNameLength {
		return false
	}
	for _, c := range name {
		if !('a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

// errorText makes the error text safe for a text column: valid UTF-8, no
// NUL, and at most maxErrorBytes bytes.
func errorText(err error) string {
	s := strings.ToValidUTF8(err.Error(), "�")
	s = strings.ReplaceAll(s, "\x00", "")
	if len(s) <= maxErrorBytes {
		return s
	}
	s = s[:maxErrorBytes]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
