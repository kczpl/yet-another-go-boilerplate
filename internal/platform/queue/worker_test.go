package queue_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/logging"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/queue"
)

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
	// The stopped run does not count toward the batch run limit.
	if n := count(t, pool, "SELECT runs FROM queue_batch_runs"); n != -1 {
		t.Errorf("stored runs = %d, want -1 until the next open", n)
	}

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
	if n := count(t, pool, "SELECT count(*) FROM queue_batch_runs"); n != 0 {
		t.Errorf("%d run counts left, want 0", n)
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

func TestRunRetriesTheStartupCheck(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	logs := newLogWatch("checking the consumers")
	w, c := newWorker(pool, logs)
	c.Handle("test.type", time.Minute, noop)
	// A closed pool fails each query, as a database restart does.
	pool.Close()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	<-logs.seen
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run = %v, want nil after the stop", err)
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
