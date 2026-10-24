package jobs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/jobs"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/logging"
	"github.com/kczpl/yet-another-go-boilerplate/internal/testdb"
)

// jobRow is the stored state of one job.
type jobRow struct {
	Status      string  `db:"status"`
	Attempts    int     `db:"attempts"`
	MaxAttempts int     `db:"max_attempts"`
	LastError   *string `db:"last_error"`
	Payload     string  `db:"payload"`
}

func newWorker(pool *pgxpool.Pool) *jobs.Worker {
	return jobs.NewWorker(pool, logging.New(io.Discard, slog.LevelInfo, logging.JSON), 1)
}

func enqueue(t *testing.T, pool *pgxpool.Pool, p jobs.Params) string {
	t.Helper()
	id, err := jobs.Enqueue(t.Context(), pool, p)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return id
}

func getJob(t *testing.T, pool *pgxpool.Pool, id string) jobRow {
	t.Helper()
	rows, _ := pool.Query(t.Context(), `
		SELECT status, attempts, max_attempts, last_error, payload::text AS payload
		FROM jobs WHERE id = $1`, id)
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[jobRow])
	if err != nil {
		t.Fatalf("getting job %s: %v", id, err)
	}
	return row
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func countJobs(t *testing.T, pool *pgxpool.Pool, where string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM jobs WHERE "+where, args...).Scan(&n); err != nil {
		t.Fatalf("counting jobs: %v", err)
	}
	return n
}

// makeDue moves the run time of a retry to now.
func makeDue(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	exec(t, pool, "UPDATE jobs SET run_at = now() WHERE id = $1", id)
}

func runDue(t *testing.T, w *jobs.Worker, want int) {
	t.Helper()
	got, err := w.RunDue(t.Context())
	if err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	if got != want {
		t.Fatalf("RunDue ran %d jobs, want %d", got, want)
	}
}

func wantJob(t *testing.T, got jobRow, status string, attempts int, lastError string) {
	t.Helper()
	gotError := ""
	if got.LastError != nil {
		gotError = *got.LastError
	}
	if got.Status != status || got.Attempts != attempts || gotError != lastError {
		t.Fatalf("job = %s, %d attempts, error %q; want %s, %d attempts, error %q",
			got.Status, got.Attempts, gotError, status, attempts, lastError)
	}
}

func TestEnqueueAndRunDeliversThePayload(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	id := enqueue(t, pool, jobs.Params{Kind: "test.echo", Payload: map[string]string{"user_id": "u-1"}})

	var got jobs.Job
	w := newWorker(pool)
	w.Handle("test.echo", time.Minute, func(_ context.Context, job jobs.Job) error {
		got = job
		return nil
	})
	runDue(t, w, 1)

	if got.ID != id || got.Kind != "test.echo" || got.Attempt != 1 {
		t.Errorf("job = %+v, want id %s, kind test.echo, attempt 1", got, id)
	}
	var payload struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(got.Payload, &payload); err != nil || payload.UserID != "u-1" {
		t.Errorf("payload = %s (%v), want user_id u-1", got.Payload, err)
	}
	wantJob(t, getJob(t, pool, id), "done", 1, "")
	runDue(t, w, 0)
}

func TestEnqueueDefaults(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	got := getJob(t, pool, enqueue(t, pool, jobs.Params{Kind: "test.defaults"}))
	if got.Payload != "{}" || got.MaxAttempts != 10 || got.Status != "queued" {
		t.Errorf("job = %+v, want payload {}, 10 max attempts, queued", got)
	}
	// A typed nil encodes as null. The handler gets {} for it too.
	var none map[string]string
	got = getJob(t, pool, enqueue(t, pool, jobs.Params{Kind: "test.defaults", Payload: none}))
	if got.Payload != "{}" {
		t.Errorf("payload of a nil map = %s, want {}", got.Payload)
	}
}

func TestEnqueueRejectsBadParams(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	tests := []struct {
		name string
		p    jobs.Params
	}{
		{"empty kind", jobs.Params{}},
		{"uppercase kind", jobs.Params{Kind: "Test.kind"}},
		{"space in kind", jobs.Params{Kind: "test kind"}},
		{"long kind", jobs.Params{Kind: strings.Repeat("a", 101)}},
		{"negative max attempts", jobs.Params{Kind: "test.kind", MaxAttempts: -1}},
		{"payload without JSON", jobs.Params{Kind: "test.kind", Payload: make(chan int)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := jobs.Enqueue(t.Context(), pool, tt.p); err == nil {
				t.Errorf("Enqueue(%+v) succeeded, want an error", tt.p)
			}
		})
	}
	if n := countJobs(t, pool, "true"); n != 0 {
		t.Errorf("%d jobs stored, want 0", n)
	}
}

func TestJobWaitsForRunAt(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	id := enqueue(t, pool, jobs.Params{Kind: "test.later", RunAt: time.Now().Add(time.Hour)})
	w := newWorker(pool)
	w.Handle("test.later", time.Minute, func(context.Context, jobs.Job) error { return nil })

	runDue(t, w, 0)
	makeDue(t, pool, id)
	runDue(t, w, 1)
}

func TestFailedJobRetriesWithBackoffThenFails(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	id := enqueue(t, pool, jobs.Params{Kind: "test.flaky", MaxAttempts: 2})
	w := newWorker(pool)
	w.Handle("test.flaky", time.Minute, func(context.Context, jobs.Job) error {
		return errors.New("boom")
	})

	runDue(t, w, 1)
	wantJob(t, getJob(t, pool, id), "queued", 1, "boom")
	// markRetry sets run_at and updated_at in one statement, so the
	// difference is the backoff: 15s plus up to 25% jitter.
	var delay float64
	err := pool.QueryRow(t.Context(),
		"SELECT extract(epoch FROM run_at - updated_at) FROM jobs WHERE id = $1", id).Scan(&delay)
	if err != nil {
		t.Fatal(err)
	}
	if delay < 15 || delay >= 18.75 {
		t.Errorf("first retry delay = %.2fs, want [15s, 18.75s)", delay)
	}
	runDue(t, w, 0)

	makeDue(t, pool, id)
	runDue(t, w, 1)
	wantJob(t, getJob(t, pool, id), "failed", 2, "boom")
	makeDue(t, pool, id)
	runDue(t, w, 0)
}

func TestPermanentErrorStopsTheRetries(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	id := enqueue(t, pool, jobs.Params{Kind: "test.bad_input"})
	w := newWorker(pool)
	w.Handle("test.bad_input", time.Minute, func(context.Context, jobs.Job) error {
		return jobs.Permanent(errors.New("bad payload"))
	})

	runDue(t, w, 1)
	wantJob(t, getJob(t, pool, id), "failed", 1, "bad payload")
}

func TestPermanentKeepsTheCause(t *testing.T) {
	t.Parallel()
	if jobs.Permanent(nil) != nil {
		t.Error("Permanent(nil) != nil")
	}
	cause := errors.New("cause")
	if err := jobs.Permanent(cause); !errors.Is(err, cause) || err.Error() != "cause" {
		t.Errorf("Permanent(cause) = %v, want a wrapper of cause", err)
	}
}

func TestPanicBecomesARetry(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	id := enqueue(t, pool, jobs.Params{Kind: "test.panic"})
	w := newWorker(pool)
	w.Handle("test.panic", time.Minute, func(context.Context, jobs.Job) error {
		panic("kaboom")
	})

	runDue(t, w, 1)
	wantJob(t, getJob(t, pool, id), "queued", 1, "job panic: kaboom")
}

func TestHandlerTimeoutBecomesARetry(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	id := enqueue(t, pool, jobs.Params{Kind: "test.slow"})
	w := newWorker(pool)
	w.Handle("test.slow", 50*time.Millisecond, func(ctx context.Context, _ jobs.Job) error {
		<-ctx.Done()
		return ctx.Err()
	})

	runDue(t, w, 1)
	wantJob(t, getJob(t, pool, id), "queued", 1, "context deadline exceeded")
}

func TestLogsOmitThePayload(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	id := enqueue(t, pool, jobs.Params{Kind: "test.private", Payload: map[string]string{"note": "s3cret"}})
	var logs bytes.Buffer
	w := jobs.NewWorker(pool, logging.New(&logs, slog.LevelDebug, logging.JSON), 1)
	w.Handle("test.private", time.Minute, func(context.Context, jobs.Job) error {
		return errors.New("boom")
	})

	runDue(t, w, 1)
	if strings.Contains(logs.String(), "s3cret") {
		t.Errorf("the logs contain the payload:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), `"job_id":"`+id+`"`) {
		t.Errorf("the logs do not name the job:\n%s", logs.String())
	}
}

func TestExpiredLeaseReturnsTheJobToTheQueue(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	lost := enqueue(t, pool, jobs.Params{Kind: "test.lease", MaxAttempts: 3})
	spent := enqueue(t, pool, jobs.Params{Kind: "test.lease", MaxAttempts: 1})
	alive := enqueue(t, pool, jobs.Params{Kind: "test.lease", MaxAttempts: 3})
	// A crashed worker leaves running rows behind.
	exec(t, pool, `
		UPDATE jobs SET status = 'running', attempts = 1, locked_until = now() - interval '1 second'
		WHERE id = ANY($1)`, []string{lost, spent})
	exec(t, pool, `
		UPDATE jobs SET status = 'running', attempts = 1, locked_until = now() + interval '1 hour'
		WHERE id = $1`, alive)

	w := newWorker(pool)
	w.Handle("test.lease", time.Minute, func(context.Context, jobs.Job) error { return nil })
	runDue(t, w, 1)

	wantJob(t, getJob(t, pool, lost), "done", 2, "")
	wantJob(t, getJob(t, pool, spent), "failed", 1, "the lease expired")
	wantJob(t, getJob(t, pool, alive), "running", 1, "")
}

// heldJob is a job that a worker runs in the background. The handler
// waits until the test sends its result.
type heldJob struct {
	result chan<- error
	done   <-chan error
	logs   *bytes.Buffer
}

// holdJob claims the next job of kind with a new worker and returns when
// the handler runs. Read logs only after done.
func holdJob(t *testing.T, pool *pgxpool.Pool, kind string) heldJob {
	t.Helper()
	started, result, done := make(chan struct{}), make(chan error), make(chan error, 1)
	logs := &bytes.Buffer{}
	w := jobs.NewWorker(pool, logging.New(logs, slog.LevelInfo, logging.JSON), 1)
	w.Handle(kind, time.Minute, func(context.Context, jobs.Job) error {
		close(started)
		return <-result
	})
	go func() {
		_, err := w.RunDue(t.Context())
		done <- err
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("the worker ran no job of %s: %v", kind, err)
	}
	return heldJob{result: result, done: done, logs: logs}
}

// finish sends the result to the handler and waits for the worker.
func (h heldJob) finish(t *testing.T, result error) {
	t.Helper()
	h.result <- result
	if err := <-h.done; err != nil {
		t.Fatalf("RunDue: %v", err)
	}
}

// expireLease moves the lease of a running job into the past.
func expireLease(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	exec(t, pool, "UPDATE jobs SET locked_until = now() - interval '1 second' WHERE id = $1", id)
}

// lateResults are the results of a worker that lost its lease. Each one
// takes another path: done, retry, and failed.
var lateResults = []struct {
	name   string
	result error
}{
	{"success", nil},
	{"error", errors.New("late failure")},
	{"permanent error", jobs.Permanent(errors.New("late failure"))},
}

func TestLateResultOfARecoveredJobIsDiscarded(t *testing.T) {
	t.Parallel()
	for _, tt := range lateResults {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pool := testdb.New(t)
			id := enqueue(t, pool, jobs.Params{Kind: "test.fence"})
			slow := holdJob(t, pool, "test.fence")

			// The cleanup of another worker returns the job to the queue.
			// The job then waits, so the slow worker does not claim it again.
			expireLease(t, pool, id)
			other := newWorker(pool)
			other.Handle("test.other", time.Minute, func(context.Context, jobs.Job) error { return nil })
			runDue(t, other, 0)
			exec(t, pool, "UPDATE jobs SET run_at = now() + interval '1 hour' WHERE id = $1", id)

			slow.finish(t, tt.result)
			wantJob(t, getJob(t, pool, id), "queued", 1, "the lease expired")
			if !strings.Contains(slow.logs.String(), "lost its lease") {
				t.Errorf("the logs do not report the lost lease:\n%s", slow.logs.String())
			}
		})
	}
}

func TestLateResultOfAClaimedJobIsDiscarded(t *testing.T) {
	t.Parallel()
	for _, tt := range lateResults {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pool := testdb.New(t)
			id := enqueue(t, pool, jobs.Params{Kind: "test.fence"})
			slow := holdJob(t, pool, "test.fence")

			// A second worker recovers the job and runs it as attempt 2.
			expireLease(t, pool, id)
			fast := holdJob(t, pool, "test.fence")

			slow.finish(t, tt.result)
			wantJob(t, getJob(t, pool, id), "running", 2, "the lease expired")
			if !strings.Contains(slow.logs.String(), "lost its lease") {
				t.Errorf("the logs do not report the lost lease:\n%s", slow.logs.String())
			}
			fast.finish(t, nil)
			wantJob(t, getJob(t, pool, id), "done", 2, "")
		})
	}
}

func TestCleanupWorksInBatches(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	n := jobs.CleanupBatch + 5
	exec(t, pool, `
		INSERT INTO jobs (kind, status, attempts, max_attempts, locked_until)
		SELECT 'test.expired', 'running', 1, 3, now() - interval '1 second'
		FROM generate_series(1, $1)`, n)
	exec(t, pool, `
		INSERT INTO jobs (kind, status, max_attempts, updated_at)
		SELECT 'test.old', 'done', 1, now() - interval '25 hours'
		FROM generate_series(1, $1)`, n)
	w := newWorker(pool)
	w.Handle("test.other", time.Minute, func(context.Context, jobs.Job) error { return nil })

	runDue(t, w, 0)
	if got := countJobs(t, pool, "kind = 'test.expired' AND status = 'queued'"); got != n {
		t.Errorf("%d expired jobs recovered, want %d", got, n)
	}
	if got := countJobs(t, pool, "kind = 'test.old'"); got != 0 {
		t.Errorf("%d old done jobs remain, want 0", got)
	}
}

func TestClaimSkipsLockedJobs(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	locked := enqueue(t, pool, jobs.Params{Kind: "test.skip"})
	free := enqueue(t, pool, jobs.Params{Kind: "test.skip"})

	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(t.Context(), "SELECT 1 FROM jobs WHERE id = $1 FOR UPDATE", locked); err != nil {
		t.Fatal(err)
	}

	var ran []string
	w := newWorker(pool)
	w.Handle("test.skip", time.Minute, func(_ context.Context, job jobs.Job) error {
		ran = append(ran, job.ID)
		return nil
	})
	runDue(t, w, 1)
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	runDue(t, w, 1)

	if len(ran) != 2 || ran[0] != free || ran[1] != locked {
		t.Errorf("ran %v, want [%s %s]", ran, free, locked)
	}
}

func TestWorkersRunEachJobOnce(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	const total = 50
	for i := range total {
		enqueue(t, pool, jobs.Params{Kind: "test.many", Payload: map[string]int{"n": i}})
	}

	// Three replicas with two claim loops each share the queue.
	ran := make(chan string, 2*total)
	ctx, cancel := context.WithCancel(t.Context())
	var replicas sync.WaitGroup
	for range 3 {
		w := jobs.NewWorker(pool, logging.New(io.Discard, slog.LevelInfo, logging.JSON), 2)
		w.Handle("test.many", time.Minute, func(_ context.Context, job jobs.Job) error {
			ran <- job.ID
			return nil
		})
		replicas.Go(func() {
			if err := w.Run(ctx); err != nil {
				t.Errorf("Run: %v", err)
			}
		})
	}

	seen := map[string]bool{}
	for range total {
		id := <-ran
		if seen[id] {
			t.Errorf("job %s ran twice", id)
		}
		seen[id] = true
	}
	cancel()
	replicas.Wait()
	close(ran)
	for id := range ran {
		t.Errorf("job %s ran again", id)
	}
	if n := countJobs(t, pool, "status = 'done'"); n != total {
		t.Errorf("%d jobs done, want %d", n, total)
	}
}

func TestEnqueueJoinsTheTransaction(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	errRollback := errors.New("rollback")
	enqueueIn := func(result error) error {
		return pgx.BeginFunc(t.Context(), pool, func(tx pgx.Tx) error {
			if _, err := jobs.Enqueue(t.Context(), tx, jobs.Params{Kind: "test.tx"}); err != nil {
				return err
			}
			return result
		})
	}

	if err := enqueueIn(errRollback); !errors.Is(err, errRollback) {
		t.Fatalf("rolled back transaction: %v", err)
	}
	if n := countJobs(t, pool, "kind = 'test.tx'"); n != 0 {
		t.Errorf("%d jobs after a rollback, want 0", n)
	}
	if err := enqueueIn(nil); err != nil {
		t.Fatalf("committed transaction: %v", err)
	}
	if n := countJobs(t, pool, "kind = 'test.tx'"); n != 1 {
		t.Errorf("%d jobs after a commit, want 1", n)
	}
}

func TestWorkerLeavesUnknownKindsInTheQueue(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	id := enqueue(t, pool, jobs.Params{Kind: "test.newer_release"})
	w := newWorker(pool)
	w.Handle("test.known", time.Minute, func(context.Context, jobs.Job) error { return nil })

	runDue(t, w, 0)
	wantJob(t, getJob(t, pool, id), "queued", 0, "")
}

func TestPruneKeepsRecentJobs(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	exec(t, pool, `
		INSERT INTO jobs (kind, status, max_attempts, updated_at) VALUES
			('test.old', 'done',   1, now() - interval '25 hours'),
			('test.new', 'done',   1, now() - interval '1 hour'),
			('test.old', 'failed', 1, now() - interval '8 days'),
			('test.new', 'failed', 1, now() - interval '6 days'),
			('test.new', 'queued', 1, now() - interval '30 days')`)
	w := newWorker(pool)
	w.Handle("test.other", time.Minute, func(context.Context, jobs.Job) error { return nil })

	runDue(t, w, 0)
	if n := countJobs(t, pool, "kind = 'test.old'"); n != 0 {
		t.Errorf("%d old finished jobs remain, want 0", n)
	}
	if n := countJobs(t, pool, "kind = 'test.new'"); n != 3 {
		t.Errorf("%d recent or queued jobs remain, want 3", n)
	}
}

// slotRecorder returns a handler that records the slot of each run.
func slotRecorder(slots *[]string) jobs.Handler {
	return func(_ context.Context, job jobs.Job) error {
		var p struct {
			Slot time.Time `json:"slot"`
		}
		if err := json.Unmarshal(job.Payload, &p); err != nil {
			return err
		}
		*slots = append(*slots, job.Kind+"@"+p.Slot.Format(time.RFC3339))
		return nil
	}
}

func TestScheduleEnqueuesEachSlotOnce(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	// The clock must be in the past: the slot is the run time of the job.
	now := time.Date(2026, 1, 5, 10, 30, 0, 0, time.UTC)
	var slots []string
	replica := func() *jobs.Worker {
		w := newWorker(pool)
		w.Handle("test.hourly", time.Minute, slotRecorder(&slots))
		w.Schedule("test.hourly", time.Hour, 0)
		jobs.SetClock(w, func() time.Time { return now })
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
	// The next slot comes soon, so a scheduled job retries less often.
	if n := countJobs(t, pool, "max_attempts = 3"); n != 2 {
		t.Errorf("%d scheduled jobs with 3 max attempts, want 2", n)
	}
}

func TestScheduleSlotsUseTheOffsetInUTC(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	var slots []string
	w := newWorker(pool)
	w.Handle("test.daily", time.Minute, slotRecorder(&slots))
	w.Handle("test.weekly", time.Minute, slotRecorder(&slots))
	w.Schedule("test.daily", 24*time.Hour, 3*time.Hour)
	w.Schedule("test.weekly", 7*24*time.Hour, 0)
	// Wednesday 02:00 UTC: the weekly slot is Monday 00:00, and the daily
	// slot is Tuesday 03:00. The worker runs the jobs in slot order.
	jobs.SetClock(w, func() time.Time { return time.Date(2026, 1, 7, 2, 0, 0, 0, time.UTC) })

	runDue(t, w, 2)
	want := "test.weekly@2026-01-05T00:00:00Z test.daily@2026-01-06T03:00:00Z"
	if got := strings.Join(slots, " "); got != want {
		t.Errorf("slots = %s, want %s", got, want)
	}
}

func TestScheduleSkipsASlotWhileAJobIsActive(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	id := enqueue(t, pool, jobs.Params{Kind: "test.hourly", RunAt: time.Now().Add(time.Hour)})
	replica := func() *jobs.Worker {
		w := newWorker(pool)
		w.Handle("test.hourly", time.Minute, func(context.Context, jobs.Job) error { return nil })
		w.Schedule("test.hourly", time.Hour, 0)
		jobs.SetClock(w, func() time.Time { return time.Date(2026, 1, 5, 10, 30, 0, 0, time.UTC) })
		return w
	}
	w := replica()

	runDue(t, w, 0)
	if n := countJobs(t, pool, "kind = 'test.hourly'"); n != 1 {
		t.Errorf("%d jobs, want only the queued one", n)
	}
	// The skipped slot does not come back after the job ends. The slot
	// is spent for all replicas.
	makeDue(t, pool, id)
	runDue(t, w, 1)
	runDue(t, w, 0)
	runDue(t, replica(), 0)
}

func TestRunEnqueuesTheScheduledSlot(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ran := make(chan jobs.Job, 1)
	w := newWorker(pool)
	w.Handle("test.hourly", time.Minute, func(_ context.Context, job jobs.Job) error {
		ran <- job
		return nil
	})
	w.Schedule("test.hourly", time.Hour, 0)
	jobs.SetClock(w, func() time.Time { return time.Date(2026, 1, 5, 10, 30, 0, 0, time.UTC) })

	stop, done := runInBackground(t, w)
	job := <-ran
	stop()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	var p struct {
		Slot time.Time `json:"slot"`
	}
	want := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)
	if err := json.Unmarshal(job.Payload, &p); err != nil || !p.Slot.Equal(want) {
		t.Errorf("payload = %s (%v), want the slot %s", job.Payload, err, want)
	}
}

// runInBackground starts w.Run and returns its cancel function and the
// channel of its result.
func runInBackground(t *testing.T, w *jobs.Worker) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	return cancel, done
}

func TestRunFinishesTheRunningJobOnShutdown(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	id := enqueue(t, pool, jobs.Params{Kind: "test.shutdown"})
	started, release := make(chan struct{}), make(chan struct{})
	w := newWorker(pool)
	w.Handle("test.shutdown", time.Minute, func(ctx context.Context, _ jobs.Job) error {
		close(started)
		<-release
		return ctx.Err()
	})

	stop, done := runInBackground(t, w)
	<-started
	stop()
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantJob(t, getJob(t, pool, id), "done", 1, "")
}

func TestRunEndsTheJobAfterTheGracePeriod(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	// A stopped run does not use up an attempt, so even the last attempt
	// returns to the queue.
	id := enqueue(t, pool, jobs.Params{Kind: "test.stuck", MaxAttempts: 1})
	started := make(chan struct{})
	w := newWorker(pool)
	jobs.SetShutdownGrace(w, 0)
	w.Handle("test.stuck", time.Hour, func(ctx context.Context, _ jobs.Job) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})

	stop, done := runInBackground(t, w)
	<-started
	stop()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantJob(t, getJob(t, pool, id), "queued", 0, "the worker stopped")
	if n := countJobs(t, pool, "id = $1 AND run_at <= now()", id); n != 1 {
		t.Error("the stopped job is not due at once")
	}
}

func TestRunNeedsAHandler(t *testing.T) {
	t.Parallel()
	if err := newWorker(nil).Run(t.Context()); err == nil {
		t.Error("Run without handlers succeeded, want an error")
	}
}

func TestRegistrationRejectsBadValues(t *testing.T) {
	t.Parallel()
	noop := func(context.Context, jobs.Job) error { return nil }
	withHandler := func() *jobs.Worker {
		w := newWorker(nil)
		w.Handle("test.kind", time.Minute, noop)
		return w
	}
	tests := []struct {
		name string
		call func()
	}{
		{"no concurrency", func() { jobs.NewWorker(nil, nil, 0) }},
		{"bad kind", func() { newWorker(nil).Handle("Bad", time.Minute, noop) }},
		{"zero timeout", func() { newWorker(nil).Handle("test.kind", 0, noop) }},
		{"nil handler", func() { newWorker(nil).Handle("test.kind", time.Minute, nil) }},
		{"second handler", func() { withHandler().Handle("test.kind", time.Minute, noop) }},
		{"schedule without handler", func() { newWorker(nil).Schedule("test.kind", time.Hour, 0) }},
		{"short interval", func() { withHandler().Schedule("test.kind", time.Second, 0) }},
		{"negative offset", func() { withHandler().Schedule("test.kind", time.Hour, -time.Second) }},
		{"offset of a full interval", func() { withHandler().Schedule("test.kind", time.Hour, time.Hour) }},
		{"second schedule", func() {
			w := withHandler()
			w.Schedule("test.kind", time.Hour, 0)
			w.Schedule("test.kind", 2*time.Hour, 0)
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
