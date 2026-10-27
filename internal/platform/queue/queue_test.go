package queue_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/logging"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/queue"
	"github.com/kczpl/yet-another-go-boilerplate/internal/testdb"
)

// testQueue and testConsumer are the subscription of most tests.
const (
	testQueue    = "test"
	testConsumer = "worker"
)

// newPool returns a database with the queue testQueue and the consumer
// testConsumer. A tick lag of 10 ms lets Run deliver a message soon after
// the send.
func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := testdb.New(t)
	exec(t, pool, "SELECT pgque.create_queue($1)", testQueue)
	exec(t, pool, "SELECT pgque.subscribe($1, $2)", testQueue, testConsumer)
	exec(t, pool, "SELECT pgque.set_queue_config($1, 'ticker_max_lag', '10 milliseconds')", testQueue)
	return pool
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return n
}

// newWorker returns a worker with one consumer on testQueue. The worker
// ticks and retries the worker lock every 10 ms.
func newWorker(pool *pgxpool.Pool, logs io.Writer) (*queue.Worker, *queue.Consumer) {
	w := queue.NewWorker(pool, logging.New(logs, slog.LevelInfo, logging.JSON))
	queue.SetIntervals(w, 10*time.Millisecond, 10*time.Millisecond)
	return w, w.Consumer(testQueue, testConsumer)
}

func send(t *testing.T, pool *pgxpool.Pool, typ string, payload any) int64 {
	t.Helper()
	id, err := queue.Send(t.Context(), pool, testQueue, typ, payload)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	return id
}

func runDue(t *testing.T, w *queue.Worker, want int) {
	t.Helper()
	got, err := w.RunDue(t.Context())
	if err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	if got != want {
		t.Fatalf("RunDue handled %d messages, want %d", got, want)
	}
}

// makeRetriesDue moves the time of each retry to now.
func makeRetriesDue(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	exec(t, pool, "UPDATE pgque.retry_queue SET ev_retry_after = now()")
}

// deadLetters returns the reason of each dead letter, in order.
func deadLetters(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, _ := pool.Query(t.Context(), "SELECT dl_reason FROM pgque.dead_letter ORDER BY dl_id")
	reasons, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("reading dead letters: %v", err)
	}
	return reasons
}

func retries(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	return count(t, pool, "SELECT count(*) FROM pgque.retry_queue")
}

func noop(context.Context, queue.Message) error { return nil }

func fail(context.Context, queue.Message) error { return errors.New("boom") }

func TestSendAndRunDeliversThePayload(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	id := send(t, pool, "test.echo", map[string]string{"user_id": "u-1"})

	var got queue.Message
	w, c := newWorker(pool, io.Discard)
	c.Handle("test.echo", time.Minute, func(_ context.Context, msg queue.Message) error {
		got = msg
		return nil
	})
	runDue(t, w, 1)

	if got.ID != id || got.Type != "test.echo" || got.Retries != 0 || got.CreatedAt.IsZero() {
		t.Errorf("message = %+v, want id %d, type test.echo, 0 retries, a creation time", got, id)
	}
	var payload struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(got.Payload, &payload); err != nil || payload.UserID != "u-1" {
		t.Errorf("payload = %s (%v), want user_id u-1", got.Payload, err)
	}
	runDue(t, w, 0)
}

func TestSendTurnsNilIntoAnEmptyObject(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	send(t, pool, "test.empty", nil)
	// A typed nil encodes as null. The handler gets {} for it too.
	var none map[string]string
	send(t, pool, "test.empty", none)

	var payloads []string
	w, c := newWorker(pool, io.Discard)
	c.Handle("test.empty", time.Minute, func(_ context.Context, msg queue.Message) error {
		payloads = append(payloads, string(msg.Payload))
		return nil
	})
	runDue(t, w, 2)
	if strings.Join(payloads, " ") != "{} {}" {
		t.Errorf("payloads = %v, want [{} {}]", payloads)
	}
}

func TestSendRejectsBadValues(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	tests := []struct {
		name    string
		queue   string
		typ     string
		payload any
	}{
		{"empty type", testQueue, "", nil},
		{"uppercase type", testQueue, "Test.type", nil},
		{"space in type", testQueue, "test type", nil},
		{"long type", testQueue, strings.Repeat("a", 58), nil},
		{"payload without JSON", testQueue, "test.type", make(chan int)},
		{"unknown queue", "nope", "test.type", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := queue.Send(t.Context(), pool, tt.queue, tt.typ, tt.payload); err == nil {
				t.Errorf("Send(%q, %q) succeeded, want an error", tt.queue, tt.typ)
			}
		})
	}
	w, c := newWorker(pool, io.Discard)
	c.Handle("test.type", time.Minute, noop)
	runDue(t, w, 0)
}

func TestSendJoinsTheTransaction(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	errRollback := errors.New("rollback")
	sendIn := func(result error) error {
		return pgx.BeginFunc(t.Context(), pool, func(tx pgx.Tx) error {
			if _, err := queue.Send(t.Context(), tx, testQueue, "test.tx", nil); err != nil {
				return err
			}
			return result
		})
	}
	w, c := newWorker(pool, io.Discard)
	c.Handle("test.tx", time.Minute, noop)

	if err := sendIn(errRollback); !errors.Is(err, errRollback) {
		t.Fatalf("rolled back transaction: %v", err)
	}
	runDue(t, w, 0)
	if err := sendIn(nil); err != nil {
		t.Fatalf("committed transaction: %v", err)
	}
	runDue(t, w, 1)
}

func TestFailedMessageRetriesWithBackoffThenGoesToTheDeadLetterQueue(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	exec(t, pool, "SELECT pgque.set_queue_config($1, 'max_retries', '1')", testQueue)
	send(t, pool, "test.flaky", nil)
	var seen []int
	var logs bytes.Buffer
	w, c := newWorker(pool, &logs)
	c.Handle("test.flaky", time.Minute, func(_ context.Context, msg queue.Message) error {
		seen = append(seen, msg.Retries)
		return errors.New("boom")
	})

	runDue(t, w, 1)
	// The first retry waits 15s plus up to 25% jitter. PgQue rounds the
	// delay to whole seconds.
	var delay float64
	err := pool.QueryRow(t.Context(),
		"SELECT extract(epoch FROM ev_retry_after - now()) FROM pgque.retry_queue").Scan(&delay)
	if err != nil {
		t.Fatal(err)
	}
	if delay < 14 || delay > 19 {
		t.Errorf("first retry delay = %.2fs, want about [15s, 19s]", delay)
	}
	runDue(t, w, 0)

	makeRetriesDue(t, pool)
	runDue(t, w, 1)
	if got := deadLetters(t, pool); len(got) != 1 || got[0] != "boom" {
		t.Errorf("dead letters = %q, want [boom]", got)
	}
	if n := retries(t, pool); n != 0 {
		t.Errorf("%d retries left, want 0", n)
	}
	runDue(t, w, 0)
	if len(seen) != 2 || seen[0] != 0 || seen[1] != 1 {
		t.Errorf("retries seen = %v, want [0 1]", seen)
	}
	// The worker predicts the decision of pgque.nack for the log. A PgQue
	// upgrade that changes the decision fails here.
	for _, want := range []string{"it will retry", "it goes to the dead letter queue"} {
		if n := strings.Count(logs.String(), want); n != 1 {
			t.Errorf("the logs contain %q %d times, want once:\n%s", want, n, logs.String())
		}
	}
}

func TestPermanentErrorSkipsTheRetries(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	send(t, pool, "test.bad_input", nil)
	w, c := newWorker(pool, io.Discard)
	c.Handle("test.bad_input", time.Minute, func(context.Context, queue.Message) error {
		return queue.Permanent(errors.New("bad payload"))
	})

	runDue(t, w, 1)
	if got := deadLetters(t, pool); len(got) != 1 || got[0] != "bad payload" {
		t.Errorf("dead letters = %q, want [bad payload]", got)
	}
	if n := retries(t, pool); n != 0 {
		t.Errorf("%d retries, want 0", n)
	}
	runDue(t, w, 0)
}

func TestPermanentKeepsTheCause(t *testing.T) {
	t.Parallel()
	if queue.Permanent(nil) != nil {
		t.Error("Permanent(nil) != nil")
	}
	cause := errors.New("cause")
	if err := queue.Permanent(cause); !errors.Is(err, cause) || err.Error() != "cause" {
		t.Errorf("Permanent(cause) = %v, want a wrapper of cause", err)
	}
}

func TestHandlerFailuresBecomeRetries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		timeout time.Duration
		handler queue.Handler
	}{
		{"panic", time.Minute, func(context.Context, queue.Message) error { panic("kaboom") }},
		{"timeout", 50 * time.Millisecond, func(ctx context.Context, _ queue.Message) error {
			<-ctx.Done()
			return ctx.Err()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pool := newPool(t)
			send(t, pool, "test.fails", nil)
			w, c := newWorker(pool, io.Discard)
			c.Handle("test.fails", tt.timeout, tt.handler)

			runDue(t, w, 1)
			if n := retries(t, pool); n != 1 {
				t.Errorf("%d retries, want 1", n)
			}
		})
	}
}

func TestMessageWithoutHandlerRetries(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	exec(t, pool, "SELECT pgque.set_queue_config($1, 'max_retries', '0')", testQueue)
	send(t, pool, "test.newer_release", nil)
	w, c := newWorker(pool, io.Discard)
	c.Handle("test.known", time.Minute, noop)

	runDue(t, w, 1)
	want := `no handler for the message type "test.newer_release"`
	if got := deadLetters(t, pool); len(got) != 1 || got[0] != want {
		t.Errorf("dead letters = %q, want [%s]", got, want)
	}
}

func TestFailureRetriesOnlyItsMessage(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	for n := range 3 {
		send(t, pool, "test.some_fail", map[string]int{"n": n})
	}
	var ran []string
	w, c := newWorker(pool, io.Discard)
	c.Handle("test.some_fail", time.Minute, func(_ context.Context, msg queue.Message) error {
		ran = append(ran, string(msg.Payload)+"#"+strconv.Itoa(msg.Retries))
		if string(msg.Payload) == `{"n": 1}` {
			return errors.New("boom")
		}
		return nil
	})

	runDue(t, w, 3)
	makeRetriesDue(t, pool)
	runDue(t, w, 1)
	want := `{"n": 0}#0 {"n": 1}#0 {"n": 2}#0 {"n": 1}#1`
	if got := strings.Join(ran, " "); got != want {
		t.Errorf("ran %s, want %s", got, want)
	}
}

func TestLogsOmitThePayload(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	id := send(t, pool, "test.private", map[string]string{"note": "s3cret"})
	var logs bytes.Buffer
	w, c := newWorker(pool, &logs)
	c.Handle("test.private", time.Minute, fail)

	runDue(t, w, 1)
	if strings.Contains(logs.String(), "s3cret") {
		t.Errorf("the logs contain the payload:\n%s", logs.String())
	}
	for _, want := range []string{`"message_id":` + strconv.FormatInt(id, 10), "message failed; it will retry"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("the logs do not contain %s:\n%s", want, logs.String())
		}
	}
}

func TestMaintenanceRotatesTheTables(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	w, c := newWorker(pool, io.Discard)
	c.Handle("test.type", time.Minute, noop)
	runDue(t, w, 0)
	// PgQue rotates when the rotation period passed and the slowest
	// consumer is past the last rotation. The xmin of a tick snapshot
	// covers the whole server, so a transaction of a parallel test can
	// hold it back. Move the last rotation to the start of time.
	exec(t, pool, `
		UPDATE pgque.queue SET queue_switch_time = now() - interval '3 hours', queue_switch_step2 = 0
		WHERE queue_name = $1`, testQueue)

	runDue(t, w, 0)
	n := count(t, pool, `
		SELECT count(*) FROM pgque.queue
		WHERE queue_name = $1 AND queue_cur_table = 1 AND queue_switch_step2 IS NOT NULL`, testQueue)
	if n != 1 {
		t.Error("the maintenance did not rotate the event table in two steps")
	}
}

func TestMaintenancePurgesOldDeadLetters(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	old := send(t, pool, "test.dead", nil)
	send(t, pool, "test.dead", nil)
	w, c := newWorker(pool, io.Discard)
	c.Handle("test.dead", time.Minute, func(context.Context, queue.Message) error {
		return queue.Permanent(errors.New("dead"))
	})
	runDue(t, w, 2)
	exec(t, pool, "UPDATE pgque.dead_letter SET dl_time = now() - interval '8 days' WHERE ev_id = $1", old)

	runDue(t, w, 0)
	if got := deadLetters(t, pool); len(got) != 1 {
		t.Errorf("%d dead letters, want the recent one", len(got))
	}
}

// openBatchAndCrash opens the next batch of testConsumer as a worker that
// crashes before it finishes the batch. runs is the stored count of the
// earlier runs that did not finish.
func openBatchAndCrash(t *testing.T, pool *pgxpool.Pool, runs int) {
	t.Helper()
	exec(t, pool, "SELECT pgque.force_next_tick($1)", testQueue)
	exec(t, pool, "SELECT pgque.ticker()")
	exec(t, pool, "SELECT pgque.next_batch($1, $2)", testQueue, testConsumer)
	if runs > 0 {
		exec(t, pool, `
			INSERT INTO queue_batch_runs (batch_id, runs)
			SELECT current_batch, $3 FROM pgque.get_consumer_info($1, $2)`, testQueue, testConsumer, runs)
	}
}

func TestBatchThatCameBackRunsAgain(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	send(t, pool, "test.crash", nil)
	openBatchAndCrash(t, pool, 0)
	var seen []int
	w, c := newWorker(pool, io.Discard)
	c.Handle("test.crash", time.Minute, func(_ context.Context, msg queue.Message) error {
		seen = append(seen, msg.Retries)
		return nil
	})

	runDue(t, w, 1)
	if len(seen) != 1 || seen[0] != 0 {
		t.Errorf("retries seen = %v, want [0]", seen)
	}
	// The finished batch deletes its count.
	if n := count(t, pool, "SELECT count(*) FROM queue_batch_runs"); n != 0 {
		t.Errorf("%d run counts left, want 0", n)
	}
}

func TestBatchThatNeverFinishesRetriesItsMessages(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	send(t, pool, "test.crash", nil)
	// Two runs crashed the worker, and the third run crashes it now.
	openBatchAndCrash(t, pool, 2)
	var seen []int
	var logs bytes.Buffer
	w, c := newWorker(pool, &logs)
	c.Handle("test.crash", time.Minute, func(_ context.Context, msg queue.Message) error {
		seen = append(seen, msg.Retries)
		return nil
	})

	// The worker does not run the handler of the batch a fourth time.
	runDue(t, w, 1)
	if len(seen) != 0 {
		t.Errorf("the handler ran %d times, want 0", len(seen))
	}
	if n := retries(t, pool); n != 1 {
		t.Errorf("%d retries, want 1", n)
	}
	if !strings.Contains(logs.String(), "batch did not finish in earlier runs") {
		t.Errorf("the logs do not report the batch:\n%s", logs.String())
	}
	// The message comes back in a new batch with one retry.
	makeRetriesDue(t, pool)
	runDue(t, w, 1)
	if len(seen) != 1 || seen[0] != 1 {
		t.Errorf("retries seen = %v, want [1]", seen)
	}
}

func TestBatchThatNeverFinishesEndsInTheDeadLetterQueue(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	exec(t, pool, "SELECT pgque.set_queue_config($1, 'max_retries', '0')", testQueue)
	send(t, pool, "test.crash", nil)
	openBatchAndCrash(t, pool, 2)
	w, c := newWorker(pool, io.Discard)
	c.Handle("test.crash", time.Minute, noop)

	runDue(t, w, 1)
	if got := deadLetters(t, pool); len(got) != 1 || got[0] != "the batch did not finish in 3 runs" {
		t.Errorf("dead letters = %q, want the batch reason", got)
	}
}
