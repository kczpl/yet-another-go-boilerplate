package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// tickInterval is the period of the PgQue ticker. The ticker of a
	// queue also waits for its tick lag (3 seconds by default) or for 500
	// new messages.
	tickInterval = time.Second
	// pollInterval is the longest wait of an idle consumer. A tick of the
	// active worker wakes the consumers earlier.
	pollInterval = 5 * time.Second
	// maintenanceInterval is the period of the retry move, the table
	// rotation, and the purge of old rows.
	maintenanceInterval = 10 * time.Second
	// scheduleInterval is the period of the schedule check.
	scheduleInterval = time.Second
	// standbyInterval is the wait of a worker that did not get the
	// worker lock before it asks again.
	standbyInterval = 5 * time.Second
	// errorDelay is the wait after a failed query, for example while the
	// database restarts.
	errorDelay = 5 * time.Second
	// stepTimeout limits one task of the active worker, for example one
	// maintenance pass. Each statement also has the statement_timeout of
	// the pool.
	stepTimeout = 30 * time.Second
	// finishTimeout limits the transaction that finishes a batch. The
	// transaction reads the batch once, so its time grows with the size
	// of the batch.
	finishTimeout = time.Minute
	// shutdownGrace is the time that the running handlers get after Run
	// ends. Then their contexts end. Keep the stop grace period of the
	// container longer (docker-compose.yml: 15s).
	shutdownGrace = 10 * time.Second
	// shutdownHardLimit is the time that the handlers get to return after
	// their contexts end.
	shutdownHardLimit = 2 * time.Second
	// keepDead is the retention time of the dead letters of a consumed
	// queue and of the run counts of old batches.
	keepDead = 7 * 24 * time.Hour
	// maxBatchRuns is the number of runs of a batch that did not finish,
	// for example after a crash. After that, the worker skips the
	// handlers and sends each message to the retry queue.
	maxBatchRuns = 3
)

var (
	errNoConsumers    = errors.New("queue: the worker has no consumers")
	errNoSubscription = errors.New("queue: no subscription")
)

// Worker runs the consumers of PgQue queues. Register all consumers,
// handlers, and schedules before Run or RunDue.
//
// The code of the worker has three files: worker.go has the registration
// and the entry points, worker_lead.go has the work of the active worker,
// and worker_batch.go has the batches of a consumer.
type Worker struct {
	db        *pgxpool.Pool
	logger    *slog.Logger
	consumers []*Consumer
	schedules []*schedule

	now     func() time.Time
	grace   time.Duration
	tick    time.Duration
	standby time.Duration
}

// Consumer receives the batches of one subscription: a consumer name on a
// queue. It must handle every message type of its queue.
type Consumer struct {
	queue    string
	name     string
	handlers map[string]registration
	// wake gets a signal after a tick, so the consumer asks for the next
	// batch at once.
	wake chan struct{}
}

type registration struct {
	handler Handler
	timeout time.Duration
}

type schedule struct {
	queue, typ    string
	every, offset time.Duration
	// last is the last slot that this worker handled. The row in
	// job_schedules is the source of truth across workers.
	last time.Time
}

// NewWorker returns a worker without consumers.
func NewWorker(db *pgxpool.Pool, logger *slog.Logger) *Worker {
	return &Worker{
		db:      db,
		logger:  logger,
		now:     time.Now,
		grace:   shutdownGrace,
		tick:    tickInterval,
		standby: standbyInterval,
	}
}

// Consumer registers the consumer name on queue and returns it. A
// migration must create the queue and subscribe the consumer
// (pgque.create_queue, pgque.subscribe): Run checks the subscription.
// Consumer panics on an invalid name or on a second registration.
func (w *Worker) Consumer(queue, name string) *Consumer {
	switch {
	case !validName(queue) || !validName(name):
		panic(fmt.Sprintf("queue: invalid consumer %q of the queue %q", name, queue))
	case slices.ContainsFunc(w.consumers, func(c *Consumer) bool { return c.queue == queue && c.name == name }):
		panic(fmt.Sprintf("queue: a second consumer %s of the queue %s", name, queue))
	}
	c := &Consumer{queue: queue, name: name, handlers: map[string]registration{}, wake: make(chan struct{}, 1)}
	w.consumers = append(w.consumers, c)
	return c
}

// Handle registers the handler of the message type typ. Each run gets
// timeout. Handle panics on an invalid type, a second handler for one
// type, or a timeout that is not positive.
func (c *Consumer) Handle(typ string, timeout time.Duration, h Handler) {
	switch {
	case !validName(typ):
		panic(fmt.Sprintf("queue: invalid message type %q", typ))
	case c.handlers[typ].handler != nil:
		panic(fmt.Sprintf("queue: a second handler for %s", typ))
	case timeout <= 0 || h == nil:
		panic(fmt.Sprintf("queue: %s needs a handler and a positive timeout", typ))
	}
	c.handlers[typ] = registration{handler: h, timeout: timeout}
}

// Schedule sends a message of type typ to queue once in each interval of
// length every. The slots start at offset after the start of each
// interval, in UTC: with every = 24h and offset = 3h, the message goes
// out daily at 03:00 UTC. A weekly interval starts on Monday.
//
// The active worker sends each slot once; the row of typ in
// job_schedules keeps the last slot across workers. A retry or a crash
// can run the message again, as for every message. A failed slot retries
// with the backoff, so with a short interval it can run after the next
// slot. The worker sends the current slot when it becomes active, but it
// does not send the slots that it missed while no worker ran. The
// payload is {"slot": "<RFC 3339>"}.
//
// Schedule panics when no consumer of queue handles typ, when typ has a
// schedule, when every is less than 1 minute, or when offset is not in
// [0, every).
func (w *Worker) Schedule(queue, typ string, every, offset time.Duration) {
	switch {
	case !slices.ContainsFunc(w.consumers, func(c *Consumer) bool { return c.queue == queue && c.handlers[typ].handler != nil }):
		panic(fmt.Sprintf("queue: schedule %s without a consumer of the queue %s that handles it", typ, queue))
	case every < time.Minute || offset < 0 || offset >= every:
		panic(fmt.Sprintf("queue: schedule %s needs every >= 1m and 0 <= offset < every", typ))
	case slices.ContainsFunc(w.schedules, func(s *schedule) bool { return s.typ == typ }):
		panic(fmt.Sprintf("queue: a second schedule for %s", typ))
	}
	w.schedules = append(w.schedules, &schedule{queue: queue, typ: typ, every: every, offset: offset})
}

// Run runs the worker until ctx ends. Only one worker of the database is
// active at a time: the worker that holds the worker lock. The active
// worker runs the PgQue ticker and maintenance, sends the schedule slots,
// and runs the consumers. Another worker waits and takes over when the
// active worker stops.
//
// When ctx ends, the consumers stop asking for batches, and the running
// handlers get up to 10 seconds. After that their contexts end. A batch
// with a stopped handler stays open and comes back to the next active
// worker as a whole. The stopped run does not count.
//
// Run returns an error when the worker has no consumer or a consumer has
// no subscription. It retries a check that fails for another reason, for
// example during a database restart.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.waitForCheck(ctx); err != nil || ctx.Err() != nil {
		return err
	}
	w.logger.Info("worker started", "consumers", w.names())
	waiting := false
	for ctx.Err() == nil {
		lock, err := tryLock(ctx, w.db)
		switch {
		case err != nil:
			w.logError(ctx, "taking the worker lock", err)
			sleep(ctx, errorDelay, nil)
		case lock == nil:
			if !waiting {
				w.logger.Info("worker waits; another worker is active")
				waiting = true
			}
			sleep(ctx, w.standby, nil)
		default:
			waiting = false
			w.lead(ctx, lock)
		}
	}
	w.logger.Info("worker stopped")
	return nil
}

// RunDue runs the work of one step of the active worker without a wait:
// it sends the due schedule slots, runs the maintenance, forces a tick on
// each consumed queue, and runs the batches of each consumer until no
// batch is ready. It returns the number of messages that it handled.
// Tests use it in place of Run. RunDue does not take the worker lock, so
// do not call it while Run runs on the same database.
func (w *Worker) RunDue(ctx context.Context) (int, error) {
	if err := w.prepareDue(ctx); err != nil {
		return 0, err
	}
	handled := 0
	for _, c := range w.consumers {
		for found := true; found; {
			n, ok, err := w.handleBatch(ctx, ctx, c)
			handled += n
			if err != nil {
				return handled, err
			}
			found = ok
		}
	}
	return handled, nil
}

// prepareDue checks the consumers, sends the due schedule slots, runs the
// maintenance, and makes a tick on each consumed queue. Each call is a
// separate transaction, so the tick sees the messages of the earlier
// calls.
func (w *Worker) prepareDue(ctx context.Context) error {
	if err := w.check(ctx); err != nil {
		return err
	}
	if err := w.sendSchedules(ctx, w.db); err != nil {
		return err
	}
	if err := w.maintain(ctx, w.db); err != nil {
		return err
	}
	for _, queue := range w.queues() {
		if err := forceTick(ctx, w.db, queue); err != nil {
			return fmt.Errorf("forcing a tick of %s: %w", queue, err)
		}
	}
	if _, err := tick(ctx, w.db); err != nil {
		return fmt.Errorf("ticking: %w", err)
	}
	return nil
}

// waitForCheck runs check until it succeeds or ctx ends. It returns a
// missing consumer and a missing subscription at once: a retry cannot fix
// them. It logs another error and tries again.
func (w *Worker) waitForCheck(ctx context.Context) error {
	for {
		err := w.check(ctx)
		switch {
		case err == nil || ctx.Err() != nil:
			return nil
		case errors.Is(err, errNoConsumers) || errors.Is(err, errNoSubscription):
			return err
		}
		w.logger.Error("checking the consumers", "error", err)
		sleep(ctx, errorDelay, nil)
	}
}

// check verifies that each consumer has a subscription.
func (w *Worker) check(ctx context.Context) error {
	if len(w.consumers) == 0 {
		return errNoConsumers
	}
	for _, c := range w.consumers {
		ok, err := subscribed(ctx, w.db, c.queue, c.name)
		if err != nil {
			return fmt.Errorf("checking the consumer %s of %s: %w", c.name, c.queue, err)
		}
		if !ok {
			return fmt.Errorf("%w for the consumer %s of the queue %s; "+
				"add a migration that calls pgque.create_queue and pgque.subscribe", errNoSubscription, c.name, c.queue)
		}
	}
	return nil
}

// queues returns the consumed queues without duplicates.
func (w *Worker) queues() []string {
	var queues []string
	for _, c := range w.consumers {
		if !slices.Contains(queues, c.queue) {
			queues = append(queues, c.queue)
		}
	}
	return queues
}

// names returns the consumers as "queue/name" for the log.
func (w *Worker) names() []string {
	names := make([]string, len(w.consumers))
	for i, c := range w.consumers {
		names[i] = c.queue + "/" + c.name
	}
	return names
}

// logError logs err. It ignores a nil error and an error after the end of
// ctx, because the shutdown cancels the queries.
func (w *Worker) logError(ctx context.Context, msg string, err error) {
	if err != nil && ctx.Err() == nil {
		w.logger.Error(msg, "error", err)
	}
}

// sleep waits for d, for a signal on wake, or until ctx ends. A nil wake
// never sends.
func sleep(ctx context.Context, d time.Duration, wake <-chan struct{}) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-wake:
	case <-t.C:
	}
}
