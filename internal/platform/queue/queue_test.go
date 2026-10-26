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
	"sync"
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
	w, c := newWorker(pool, io.Discard)
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
	// The consumer must pass a tick after the creation of the queue.
	runDue(t, w, 0)
	exec(t, pool, "UPDATE pgque.queue SET queue_switch_time = now() - interval '3 hours' WHERE queue_name = $1", testQueue)

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

// slotRecorder returns a handler that records the slot of each message.
func slotRecorder(slots *[]string) queue.Handler {
	return func(_ context.Context, msg queue.Message) error {
		var p struct {
			Slot time.Time `json:"slot"`
		}
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return err
		}
		*slots = append(*slots, msg.Type+"@"+p.Slot.Format(time.RFC3339))
		return nil
	}
}

func TestScheduleSendsEachSlotOnce(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	now := time.Date(2026, 1, 5, 10, 30, 0, 0, time.UTC)
	var slots []string
	replica := func() *queue.Worker {
		w, c := newWorker(pool, io.Discard)
		c.Handle("test.hourly", time.Minute, slotRecorder(&slots))
		w.Schedule(testQueue, "test.hourly", time.Hour, 0)
		queue.SetClock(w, func() time.Time { return now })
		return w
	}
	a, b := replica(), replica()

	runDue(t, a, 1)
	runDue(t, b, 0)
	now = now.Add(time.Hour)
	runDue(t, b, 1)
	runDue(t, a, 0)

	want := []string{"test.hourly@2026-01-05T10:00:00Z", "test.hourly@2026-01-05T11:00:00Z"}
	if strings.Join(slots, " ") != strings.Join(want, " ") {
		t.Errorf("slots = %v, want %v", slots, want)
	}
}

func TestScheduleSlotsUseTheOffsetInUTC(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	var slots []string
	w, c := newWorker(pool, io.Discard)
	c.Handle("test.daily", time.Minute, slotRecorder(&slots))
	c.Handle("test.weekly", time.Minute, slotRecorder(&slots))
	w.Schedule(testQueue, "test.daily", 24*time.Hour, 3*time.Hour)
	w.Schedule(testQueue, "test.weekly", 7*24*time.Hour, 0)
	// Wednesday 02:00 UTC: the daily slot is Tuesday 03:00, and the weekly
	// slot is Monday 00:00.
	queue.SetClock(w, func() time.Time { return time.Date(2026, 1, 7, 2, 0, 0, 0, time.UTC) })

	runDue(t, w, 2)
	want := "test.daily@2026-01-06T03:00:00Z test.weekly@2026-01-05T00:00:00Z"
	if got := strings.Join(slots, " "); got != want {
		t.Errorf("slots = %s, want %s", got, want)
	}
}

// logWatch is a log writer for a worker in the background. seen closes
// when the log first contains want.
type logWatch struct {
	mu   sync.Mutex
	buf  strings.Builder
	want string
	seen chan struct{}
}

func newLogWatch(want string) *logWatch {
	return &logWatch{want: want, seen: make(chan struct{})}
}

func (l *logWatch) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	had := strings.Contains(l.buf.String(), l.want)
	l.buf.Write(p)
	if !had && strings.Contains(l.buf.String(), l.want) {
		close(l.seen)
	}
	return len(p), nil
}

func (l *logWatch) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// runInBackground starts w.Run and returns its stop function. The stop
// function waits for Run and fails the test on an error.
func runInBackground(t *testing.T, w *queue.Worker) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	return func() {
		t.Helper()
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	}
}

func TestRunSendsTheScheduledSlot(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	ran := make(chan queue.Message, 1)
	w, c := newWorker(pool, io.Discard)
	c.Handle("test.hourly", time.Minute, func(_ context.Context, msg queue.Message) error {
		ran <- msg
		return nil
	})
	w.Schedule(testQueue, "test.hourly", time.Hour, 0)
	queue.SetClock(w, func() time.Time { return time.Date(2026, 1, 5, 10, 30, 0, 0, time.UTC) })

	stop := runInBackground(t, w)
	msg := <-ran
	stop()
	var p struct {
		Slot time.Time `json:"slot"`
	}
	want := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)
	if err := json.Unmarshal(msg.Payload, &p); err != nil || !p.Slot.Equal(want) {
		t.Errorf("payload = %s (%v), want the slot %s", msg.Payload, err, want)
	}
}

func TestOneWorkerIsActive(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	const total = 50
	for n := range total {
		send(t, pool, "test.many", map[string]int{"n": n})
	}

	// Three replicas share the queue. One of them is active.
	ran := make(chan int64, 2*total)
	var stops []func()
	var logs []*logWatch
	for range 3 {
		l := newLogWatch(`"msg":"worker is active"`)
		w, c := newWorker(pool, l)
		c.Handle("test.many", time.Minute, func(_ context.Context, msg queue.Message) error {
			ran <- msg.ID
			return nil
		})
		logs = append(logs, l)
		stops = append(stops, runInBackground(t, w))
	}

	seen := map[int64]bool{}
	for range total {
		id := <-ran
		if seen[id] {
			t.Errorf("message %d ran twice", id)
		}
		seen[id] = true
	}
	active := 0
	for _, l := range logs {
		active += strings.Count(l.String(), `"msg":"worker is active"`)
	}
	if active != 1 {
		t.Errorf("%d workers became active, want 1", active)
	}
	for _, stop := range stops {
		stop()
	}
	close(ran)
	for id := range ran {
		t.Errorf("message %d ran again", id)
	}
}

func TestWaitingWorkerTakesOver(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	ran := make(chan string, 2)
	replica := func(name string, logs io.Writer) *queue.Worker {
		w, c := newWorker(pool, logs)
		c.Handle("test.takeover", time.Minute, func(context.Context, queue.Message) error {
			ran <- name
			return nil
		})
		return w
	}

	send(t, pool, "test.takeover", nil)
	stopFirst := runInBackground(t, replica("first", io.Discard))
	if got := <-ran; got != "first" {
		t.Fatalf("%s handled the first message, want first", got)
	}
	waits := newLogWatch("another worker is active")
	stopSecond := runInBackground(t, replica("second", waits))
	<-waits.seen

	stopFirst()
	send(t, pool, "test.takeover", nil)
	if got := <-ran; got != "second" {
		t.Errorf("%s handled the second message, want second", got)
	}
	stopSecond()
}

func TestRunFinishesTheBatchOnShutdown(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	send(t, pool, "test.shutdown", nil)
	started, release := make(chan struct{}), make(chan struct{})
	w, c := newWorker(pool, io.Discard)
	c.Handle("test.shutdown", time.Minute, func(context.Context, queue.Message) error {
		close(started)
		<-release
		return nil
	})

	stop := runInBackground(t, w)
	<-started
	go close(release)
	stop()

	after, c := newWorker(pool, io.Discard)
	c.Handle("test.shutdown", time.Minute, noop)
	runDue(t, after, 0)
}

func TestRunReturnsTheBatchAfterTheGracePeriod(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	// A stopped run does not count as a retry, so even the last delivery
	// returns to the queue.
	exec(t, pool, "SELECT pgque.set_queue_config($1, 'max_retries', '0')", testQueue)
	send(t, pool, "test.stuck", nil)
	started := make(chan struct{})
	w, c := newWorker(pool, io.Discard)
	queue.SetShutdownGrace(w, 0)
	c.Handle("test.stuck", time.Hour, func(ctx context.Context, _ queue.Message) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})

	stop := runInBackground(t, w)
	<-started
	stop()

	var retriesSeen []int
	after, c := newWorker(pool, io.Discard)
	c.Handle("test.stuck", time.Minute, func(_ context.Context, msg queue.Message) error {
		retriesSeen = append(retriesSeen, msg.Retries)
		return nil
	})
	runDue(t, after, 1)
	if len(retriesSeen) != 1 || retriesSeen[0] != 0 {
		t.Errorf("retries seen = %v, want [0]", retriesSeen)
	}
	if got := deadLetters(t, pool); len(got) != 0 {
		t.Errorf("dead letters = %q, want none", got)
	}
}

func TestLostLockStopsTheHandlers(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	send(t, pool, "test.lost", nil)
	started, stopped := make(chan struct{}), make(chan struct{})
	again := make(chan int, 1)
	runs := 0
	w, c := newWorker(pool, io.Discard)
	c.Handle("test.lost", time.Hour, func(ctx context.Context, msg queue.Message) error {
		runs++
		if runs > 1 {
			again <- msg.Retries
			return nil
		}
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	})

	stop := runInBackground(t, w)
	<-started
	// The server ends the session of the worker lock, as after a network
	// failure. The grace period of a shutdown does not apply.
	exec(t, pool, `
		SELECT pg_terminate_backend(pid) FROM pg_locks
		WHERE locktype = 'advisory' AND objid = $1
		  AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`,
		queue.WorkerLockID)
	<-stopped
	// The worker takes the lock again and gets the open batch again.
	if retries := <-again; retries != 0 {
		t.Errorf("the second run saw %d retries, want 0", retries)
	}
	stop()
}

func TestRunNeedsAConsumerWithASubscription(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	tests := []struct {
		name     string
		queue    string
		consumer string
	}{
		{"unknown consumer", testQueue, "nobody"},
		{"unknown queue", "nope", testConsumer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := queue.NewWorker(pool, logging.New(io.Discard, slog.LevelInfo, logging.JSON))
			w.Consumer(tt.queue, tt.consumer).Handle("test.type", time.Minute, noop)
			if err := w.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "no subscription") {
				t.Errorf("Run = %v, want a subscription error", err)
			}
			if _, err := w.RunDue(t.Context()); err == nil {
				t.Error("RunDue succeeded, want an error")
			}
		})
	}
	if err := queue.NewWorker(nil, nil).Run(t.Context()); err == nil {
		t.Error("Run without consumers succeeded, want an error")
	}
}

func TestRunStopsDuringTheStartupCheck(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	w, c := newWorker(pool, io.Discard)
	c.Handle("test.type", time.Minute, noop)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := w.Run(ctx); err != nil {
		t.Errorf("Run after a stop = %v, want nil", err)
	}
}

func TestRegistrationRejectsBadValues(t *testing.T) {
	t.Parallel()
	newW := func() *queue.Worker { return queue.NewWorker(nil, nil) }
	withHandler := func() *queue.Worker {
		w := newW()
		w.Consumer(testQueue, testConsumer).Handle("test.type", time.Minute, noop)
		return w
	}
	tests := []struct {
		name string
		call func()
	}{
		{"bad queue", func() { newW().Consumer("Test", testConsumer) }},
		{"bad consumer", func() { newW().Consumer(testQueue, "") }},
		{"second consumer", func() { withHandler().Consumer(testQueue, testConsumer) }},
		{"bad type", func() { newW().Consumer(testQueue, testConsumer).Handle("Bad", time.Minute, noop) }},
		{"zero timeout", func() { newW().Consumer(testQueue, testConsumer).Handle("test.type", 0, noop) }},
		{"nil handler", func() { newW().Consumer(testQueue, testConsumer).Handle("test.type", time.Minute, nil) }},
		{"second handler", func() {
			c := newW().Consumer(testQueue, testConsumer)
			c.Handle("test.type", time.Minute, noop)
			c.Handle("test.type", time.Minute, noop)
		}},
		{"schedule without handler", func() { withHandler().Schedule(testQueue, "test.other", time.Hour, 0) }},
		{"schedule on another queue", func() { withHandler().Schedule("other", "test.type", time.Hour, 0) }},
		{"short interval", func() { withHandler().Schedule(testQueue, "test.type", time.Second, 0) }},
		{"negative offset", func() { withHandler().Schedule(testQueue, "test.type", time.Hour, -time.Second) }},
		{"offset of a full interval", func() { withHandler().Schedule(testQueue, "test.type", time.Hour, time.Hour) }},
		{"second schedule", func() {
			w := withHandler()
			w.Schedule(testQueue, "test.type", time.Hour, 0)
			w.Schedule(testQueue, "test.type", 2*time.Hour, 0)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if recover() == nil {
					t.Errorf("no panic")
				}
			}()
			tt.call()
		})
	}
}
