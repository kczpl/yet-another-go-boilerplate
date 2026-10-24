package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/logging"
)

const (
	// pollInterval is the wait of an idle claim loop before it asks again.
	pollInterval = time.Second
	// errorDelay is the wait after a failed claim, for example while the
	// database restarts.
	errorDelay = 5 * time.Second
	// cleanupInterval is the period of the lease recovery and the prune.
	cleanupInterval = time.Minute
	// leaseMargin extends each lease past the handler timeout. The handler
	// then has time to return and to store its result.
	leaseMargin = 30 * time.Second
	// storeTimeout limits the statement that stores the result of a job.
	storeTimeout = 5 * time.Second
	// shutdownGrace is the time that running jobs get after Run ends.
	// Then their contexts end. Keep the stop grace period of the container
	// longer (docker-compose.yml: 15s).
	shutdownGrace = 10 * time.Second
	// shutdownHardLimit is the time that the handlers get to return after
	// their contexts end.
	shutdownHardLimit = 2 * time.Second

	// keepDone and keepFailed are the retention times of finished jobs.
	keepDone   = 24 * time.Hour
	keepFailed = 7 * 24 * time.Hour

	// scheduleMaxAttempts caps the runs of a scheduled job. The next slot
	// enqueues a new job anyway.
	scheduleMaxAttempts = 3
	// maxErrorBytes caps the error text that a job stores.
	maxErrorBytes = 2000
)

// Worker claims and runs jobs. Register all handlers and schedules before
// Run or RunDue.
type Worker struct {
	db          *pgxpool.Pool
	logger      *slog.Logger
	concurrency int

	handlers  map[string]registration
	kinds     []string
	leaseMS   []int64
	schedules []*schedule

	now   func() time.Time
	grace time.Duration
}

type registration struct {
	handler Handler
	timeout time.Duration
}

type schedule struct {
	kind          string
	every, offset time.Duration
	// last is the last slot that this worker handled. The database row in
	// job_schedules is the source of truth across replicas.
	last time.Time
}

// NewWorker returns a worker that runs at most concurrency jobs at once.
// It panics when concurrency is less than 1.
func NewWorker(db *pgxpool.Pool, logger *slog.Logger, concurrency int) *Worker {
	if concurrency < 1 {
		panic(fmt.Sprintf("jobs: concurrency %d is less than 1", concurrency))
	}
	return &Worker{
		db:          db,
		logger:      logger,
		concurrency: concurrency,
		handlers:    map[string]registration{},
		now:         time.Now,
		grace:       shutdownGrace,
	}
}

// Handle registers the handler of kind. Each run gets timeout. The worker
// claims only the kinds that it handles, so an old replica leaves a new
// kind in the queue during a rolling deploy. Handle panics on an invalid
// kind, a second handler for one kind, or a timeout that is not positive.
func (w *Worker) Handle(kind string, timeout time.Duration, h Handler) {
	switch {
	case !validKind(kind):
		panic(fmt.Sprintf("jobs: invalid kind %q", kind))
	case w.handlers[kind].handler != nil:
		panic(fmt.Sprintf("jobs: a second handler for %s", kind))
	case timeout <= 0 || h == nil:
		panic(fmt.Sprintf("jobs: %s needs a handler and a positive timeout", kind))
	}
	w.handlers[kind] = registration{handler: h, timeout: timeout}
	w.kinds = append(w.kinds, kind)
	w.leaseMS = append(w.leaseMS, (timeout + leaseMargin).Milliseconds())
}

// Schedule enqueues a job of kind once in each interval of length every.
// The slots start at offset after the start of each interval, in UTC: with
// every = 24h and offset = 3h, the job runs daily at 03:00 UTC. A weekly
// interval starts on Monday.
//
// Each slot enqueues at most one job across all replicas. A retry or a
// crash can run that job again, as for every job. The worker enqueues
// the current slot when it starts, but it does not enqueue the slots that
// it missed while it was down. A slot is skipped while a job of the kind
// is queued or running. The payload of the job is {"slot": "<RFC 3339>"}.
//
// Schedule panics when kind has no handler, when kind has a schedule,
// when every is less than 1 minute, or when offset is not in [0, every).
func (w *Worker) Schedule(kind string, every, offset time.Duration) {
	switch {
	case w.handlers[kind].handler == nil:
		panic(fmt.Sprintf("jobs: schedule %s without a handler", kind))
	case every < time.Minute || offset < 0 || offset >= every:
		panic(fmt.Sprintf("jobs: schedule %s needs every >= 1m and 0 <= offset < every", kind))
	}
	for _, s := range w.schedules {
		if s.kind == kind {
			panic(fmt.Sprintf("jobs: a second schedule for %s", kind))
		}
	}
	w.schedules = append(w.schedules, &schedule{kind: kind, every: every, offset: offset})
}

// Run claims and runs jobs until ctx ends. Then it stops the claims and
// gives the running jobs up to 10 seconds to finish. After that it ends
// their contexts. A job that then returns an error goes back to the queue
// at once, and the stopped run does not use up an attempt.
func (w *Worker) Run(ctx context.Context) error {
	if len(w.kinds) == 0 {
		return errors.New("jobs: the worker has no handlers")
	}
	// The jobs do not end with ctx. They get the grace period first.
	jobCtx, stopJobs := context.WithCancel(context.WithoutCancel(ctx))
	defer stopJobs()

	var running sync.WaitGroup
	for range w.concurrency {
		running.Go(func() { w.claimLoop(ctx, jobCtx) })
	}
	w.logger.Info("worker started", "concurrency", w.concurrency, "kinds", w.kinds)
	w.maintain(ctx)

	w.logger.Info("worker stopping")
	w.drain(&running, stopJobs)
	return nil
}

// RunDue enqueues the due schedule slots and runs the cleanup. Then it
// runs the due jobs one at a time until no job is due, and returns the
// number of jobs that it ran. Tests use it in place of Run, because it
// returns without a wait. Do not call RunDue while Run runs on the same
// Worker.
func (w *Worker) RunDue(ctx context.Context) (int, error) {
	if err := w.enqueueSchedules(ctx); err != nil {
		return 0, err
	}
	if err := w.cleanup(ctx); err != nil {
		return 0, err
	}
	ran := 0
	for {
		found, err := w.runNext(ctx)
		if err != nil || !found {
			return ran, err
		}
		ran++
	}
}

// claimLoop claims and runs jobs until ctx ends. The claim and the job use
// jobCtx, not ctx. A claim that ctx cancels can still commit on the
// server, and its job then stays in the running state until the lease
// expires.
func (w *Worker) claimLoop(ctx, jobCtx context.Context) {
	for ctx.Err() == nil {
		found, err := w.runNext(jobCtx)
		var wait time.Duration
		switch {
		case err != nil:
			w.logger.Error("claiming a job", "error", err)
			wait = errorDelay
		case !found:
			wait = pollInterval
		}
		if !sleep(ctx, wait) {
			return
		}
	}
}

// maintain enqueues the schedule slots every second and runs the cleanup
// every minute, until ctx ends.
func (w *Worker) maintain(ctx context.Context) {
	schedules := time.NewTicker(time.Second)
	defer schedules.Stop()
	cleanups := time.NewTicker(cleanupInterval)
	defer cleanups.Stop()

	w.logError(ctx, "enqueueing scheduled jobs", w.enqueueSchedules(ctx))
	w.logError(ctx, "cleaning up jobs", w.cleanup(ctx))
	for {
		select {
		case <-ctx.Done():
			return
		case <-schedules.C:
			w.logError(ctx, "enqueueing scheduled jobs", w.enqueueSchedules(ctx))
		case <-cleanups.C:
			w.logError(ctx, "cleaning up jobs", w.cleanup(ctx))
		}
	}
}

// logError logs err. It ignores a nil error and an error after the end of
// ctx, because the shutdown cancels the queries.
func (w *Worker) logError(ctx context.Context, msg string, err error) {
	if err != nil && ctx.Err() == nil {
		w.logger.Error(msg, "error", err)
	}
}

// drain waits for the claim loops. When the grace period ends first, it
// ends the job contexts and waits a short time more.
func (w *Worker) drain(running *sync.WaitGroup, stopJobs context.CancelFunc) {
	done := make(chan struct{})
	go func() {
		running.Wait()
		close(done)
	}()
	select {
	case <-done:
		w.logger.Info("worker stopped")
		return
	case <-time.After(w.grace):
	}
	stopJobs()
	select {
	case <-done:
		w.logger.Info("worker stopped; it ended the running jobs")
	case <-time.After(shutdownHardLimit):
		w.logger.Error("worker stopped; a handler ignored the end of its context")
	}
}

// runNext claims one due job and runs it. It returns false when no job is
// due.
func (w *Worker) runNext(ctx context.Context) (bool, error) {
	job, found, err := claim(ctx, w.db, w.kinds, w.leaseMS)
	if err != nil || !found {
		return false, err
	}
	w.execute(ctx, job)
	return true, nil
}

// execute runs the handler of job and stores the result.
func (w *Worker) execute(ctx context.Context, job Job) {
	ctx = logging.WithAttrs(ctx,
		slog.String("job_id", job.ID),
		slog.String("job_kind", job.Kind),
		slog.Int("job_attempt", job.Attempt))
	start := time.Now()
	jobErr := runHandler(ctx, w.handlers[job.Kind], job)
	took := time.Since(start)
	// ctx ends only when the worker stops. Then the job did not fail.
	stopped := ctx.Err() != nil

	// The result must reach the database also after ctx ends.
	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeTimeout)
	defer cancel()
	w.store(storeCtx, job, jobErr, took, stopped)
}

// runHandler runs the handler with its timeout. It turns a panic into an
// error, so the job retries and the worker continues.
func runHandler(ctx context.Context, reg registration, job Job) (err error) {
	ctx, cancel := context.WithTimeout(ctx, reg.timeout)
	defer cancel()
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("job panic: %v", p)
		}
	}()
	return reg.handler(ctx, job)
}

// store records the result of one attempt and logs it. When the worker
// stopped the job, an error that is not permanent returns the job to the
// queue. It never logs the payload.
func (w *Worker) store(ctx context.Context, job Job, jobErr error, took time.Duration, stopped bool) {
	var (
		stored bool
		err    error
		level  = slog.LevelInfo
		msg    = "job done"
		attrs  = []any{"duration", took}
	)
	switch {
	case jobErr == nil:
		stored, err = markDone(ctx, w.db, job)
	case stopped && !isPermanent(jobErr):
		stored, err = release(ctx, w.db, job)
		msg = "job stopped; it returns to the queue"
	case isPermanent(jobErr) || job.Attempt >= job.maxAttempts:
		stored, err = markFailed(ctx, w.db, job, errorText(jobErr))
		level, msg = slog.LevelError, "job failed"
		attrs = append(attrs, "error", jobErr)
	default:
		delay := backoff(job.Attempt)
		stored, err = markRetry(ctx, w.db, job, delay, errorText(jobErr))
		level, msg = slog.LevelWarn, "job failed; it will retry"
		attrs = append(attrs, "error", jobErr, "retry_in", delay)
	}
	switch {
	case err != nil:
		w.logger.ErrorContext(ctx, "storing the job result", "error", err)
	case !stored:
		w.logger.WarnContext(ctx, "the job lost its lease; the result is discarded")
	default:
		w.logger.Log(ctx, level, msg, attrs...)
	}
}

// enqueueSchedules enqueues the current slot of each schedule once.
func (w *Worker) enqueueSchedules(ctx context.Context) error {
	now := w.now()
	for _, s := range w.schedules {
		slot := now.Add(-s.offset).Truncate(s.every).Add(s.offset).UTC()
		if !slot.After(s.last) {
			continue
		}
		payload, err := json.Marshal(map[string]time.Time{"slot": slot})
		if err != nil {
			return err
		}
		added, err := enqueueSlot(ctx, w.db, s.kind, slot, payload, scheduleMaxAttempts)
		if err != nil {
			return fmt.Errorf("enqueueing schedule %s: %w", s.kind, err)
		}
		s.last = slot
		if added {
			w.logger.Info("scheduled job enqueued", "job_kind", s.kind, "slot", slot)
		}
	}
	return nil
}

// cleanup returns the jobs with an expired lease to the queue and deletes
// the old finished jobs.
func (w *Worker) cleanup(ctx context.Context) error {
	recovered, err := recoverExpired(ctx, w.db)
	if err != nil {
		return fmt.Errorf("recovering expired jobs: %w", err)
	}
	if recovered > 0 {
		w.logger.Warn("jobs recovered after an expired lease", "count", recovered)
	}
	if _, err := prune(ctx, w.db, keepDone, keepFailed); err != nil {
		return fmt.Errorf("pruning jobs: %w", err)
	}
	return nil
}

// backoff returns the delay before the next attempt: 15s, 30s, 1m, and so
// on up to 1h, plus up to 25% random jitter. The jitter spreads the
// retries of jobs that failed together.
func backoff(attempt int) time.Duration {
	d := min(15*time.Second<<min(max(attempt-1, 0), 10), time.Hour)
	return d + rand.N(d/4)
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

// sleep waits for d or until ctx ends. It returns false when ctx ended.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
