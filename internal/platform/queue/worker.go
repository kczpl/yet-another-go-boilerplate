package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/logging"
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
	// rotation, and the dead letter purge.
	maintenanceInterval = 10 * time.Second
	// scheduleInterval is the period of the schedule check.
	scheduleInterval = time.Second
	// standbyInterval is the wait of a worker that did not get the
	// worker lock before it asks again.
	standbyInterval = 5 * time.Second
	// errorDelay is the wait after a failed query, for example while the
	// database restarts.
	errorDelay = 5 * time.Second
	// storeTimeout limits a statement that must complete after a stop,
	// for example the statement that finishes a batch.
	storeTimeout = 5 * time.Second
	// stepTimeout limits one task of the active worker, for example one
	// maintenance pass. Each statement also has the statement_timeout of
	// the pool. A cancel in the middle of a query closes the lock
	// connection, so keep this limit well above the normal duration.
	stepTimeout = 30 * time.Second
	// shutdownGrace is the time that the running handlers get after Run
	// ends. Then their contexts end. Keep the stop grace period of the
	// container longer (docker-compose.yml: 15s).
	shutdownGrace = 10 * time.Second
	// shutdownHardLimit is the time that the handlers get to return after
	// their contexts end.
	shutdownHardLimit = 2 * time.Second
	// keepDead is the retention time of the dead letters of a consumed
	// queue.
	keepDead = 7 * 24 * time.Hour
)

// Worker runs the consumers of PgQue queues. Register all consumers,
// handlers, and schedules before Run or RunDue.
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

// result is the result of one message.
type result struct {
	msg   Message
	err   error
	took  time.Duration
	delay time.Duration
	dead  bool
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
// can run the message again, as for every message. The worker sends the
// current slot when it becomes active, but it does not send the slots
// that it missed while no worker ran. The payload is
// {"slot": "<RFC 3339>"}.
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
// worker as a whole. The stopped run does not count as a retry.
//
// Run returns an error when the worker has no consumer or a consumer has
// no subscription. It returns nil when ctx ends during that check.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.check(ctx); err != nil {
		if len(w.consumers) > 0 && ctx.Err() != nil {
			return nil
		}
		return err
	}
	w.logger.Info("worker started", "consumers", w.names())
	waiting := false
	for ctx.Err() == nil {
		lock, err := tryLock(ctx, w.db)
		switch {
		case err != nil:
			w.logError(ctx, "taking the worker lock", err)
			sleep(ctx, errorDelay)
		case lock == nil:
			if !waiting {
				w.logger.Info("worker waits; another worker is active")
				waiting = true
			}
			sleep(ctx, w.standby)
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

// check verifies that each consumer has a subscription.
func (w *Worker) check(ctx context.Context) error {
	if len(w.consumers) == 0 {
		return errors.New("queue: the worker has no consumers")
	}
	for _, c := range w.consumers {
		ok, err := subscribed(ctx, w.db, c.queue, c.name)
		if err != nil {
			return fmt.Errorf("checking the consumer %s of %s: %w", c.name, c.queue, err)
		}
		if !ok {
			return fmt.Errorf("queue: the consumer %s of the queue %s has no subscription; "+
				"add a migration that calls pgque.create_queue and pgque.subscribe", c.name, c.queue)
		}
	}
	return nil
}

// lead runs the active worker until ctx ends or the lock connection
// fails. It holds the lock until its handlers stopped, so another worker
// cannot start the same batch during the drain.
func (w *Worker) lead(ctx context.Context, lock *pgxpool.Conn) {
	defer unlock(lock)
	w.logger.Info("worker is active")

	// The handlers do not end with ctx. They get the grace period first.
	consumeCtx, stopConsumers := context.WithCancel(ctx)
	defer stopConsumers()
	handlerCtx, stopHandlers := context.WithCancel(context.WithoutCancel(ctx))
	defer stopHandlers()

	var running sync.WaitGroup
	for _, c := range w.consumers {
		running.Go(func() { w.consume(consumeCtx, handlerCtx, c) })
	}
	lost := w.leadLoop(ctx, lock)
	stopConsumers()
	if lost {
		// Another worker can be active now. Stop the handlers at once.
		w.logger.Error("worker lost the worker lock; it stops the running handlers")
		stopHandlers()
	}
	w.drain(&running, stopHandlers)
}

// leadLoop runs the ticker, the schedules, and the maintenance on the
// lock connection until ctx ends. It returns true when the lock
// connection failed.
func (w *Worker) leadLoop(ctx context.Context, lock *pgxpool.Conn) bool {
	ticks := time.NewTicker(w.tick)
	defer ticks.Stop()
	slots := time.NewTicker(scheduleInterval)
	defer slots.Stop()
	maintenance := time.NewTicker(maintenanceInterval)
	defer maintenance.Stop()

	if !w.step(ctx, lock, "running the maintenance", w.maintain) {
		return true
	}
	for {
		var ok bool
		select {
		case <-ctx.Done():
			return false
		case <-ticks.C:
			ok = w.step(ctx, lock, "ticking", w.tickAndWake)
		case <-slots.C:
			ok = w.step(ctx, lock, "sending scheduled messages", w.sendSchedules)
		case <-maintenance.C:
			ok = w.step(ctx, lock, "running the maintenance", w.maintain)
		}
		if !ok {
			return true
		}
	}
}

// step runs one task of the active worker on the lock connection. The
// task has its own timeout and does not end with ctx: a cancel in the
// middle of a query closes the connection, and the lock with it. It
// returns false when the lock connection is closed.
func (w *Worker) step(ctx context.Context, lock *pgxpool.Conn, msg string, task func(context.Context, querier) error) bool {
	taskCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stepTimeout)
	defer cancel()
	if err := task(taskCtx, lock); err != nil {
		w.logger.Error(msg, "error", err)
	}
	return !lock.Conn().IsClosed()
}

// tickAndWake runs the ticker and wakes the consumers after a tick.
func (w *Worker) tickAndWake(ctx context.Context, db querier) error {
	n, err := tick(ctx, db)
	if err != nil || n == 0 {
		return err
	}
	for _, c := range w.consumers {
		select {
		case c.wake <- struct{}{}:
		default:
		}
	}
	return nil
}

// maintain moves the due retries back to their queues, rotates the event
// tables, and deletes the old dead letters of the consumed queues.
func (w *Worker) maintain(ctx context.Context, db querier) error {
	if err := moveRetries(ctx, db); err != nil {
		return fmt.Errorf("moving retries: %w", err)
	}
	if err := rotate(ctx, db); err != nil {
		return fmt.Errorf("rotating tables: %w", err)
	}
	for _, queue := range w.queues() {
		if err := purgeDead(ctx, db, queue, keepDead); err != nil {
			return fmt.Errorf("purging the dead letters of %s: %w", queue, err)
		}
	}
	return nil
}

// sendSchedules sends the current slot of each schedule once.
func (w *Worker) sendSchedules(ctx context.Context, db querier) error {
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
		sent, err := sendSlot(ctx, db, s.queue, s.typ, slot, payload)
		if err != nil {
			return fmt.Errorf("sending the schedule %s: %w", s.typ, err)
		}
		s.last = slot
		if sent {
			w.logger.Info("scheduled message sent", "queue", s.queue, "message_type", s.typ, "slot", slot)
		}
	}
	return nil
}

// consume runs the batches of c until ctx ends. The handlers use
// handlerCtx, which ends later than ctx.
func (w *Worker) consume(ctx, handlerCtx context.Context, c *Consumer) {
	for ctx.Err() == nil {
		_, found, err := w.handleBatch(ctx, handlerCtx, c)
		switch {
		case err != nil:
			w.logError(ctx, "handling a batch", err)
			sleep(ctx, errorDelay)
		case !found:
			w.waitForTick(ctx, c)
		}
	}
}

// waitForTick waits for a tick, for the poll interval, or for the end of
// ctx.
func (w *Worker) waitForTick(ctx context.Context, c *Consumer) {
	t := time.NewTimer(pollInterval)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-c.wake:
	case <-t.C:
	}
}

// handleBatch opens the next batch of c, runs the handlers, and finishes
// the batch. It opens and reads the batch with ctx and runs the handlers
// with handlerCtx. A batch that ctx stops before the handlers stays open,
// and that is safe. It returns the number of messages, and false when no
// batch is ready. When handlerCtx ends during the batch and a handler
// failed, the batch stays open: the next batch call delivers it again.
func (w *Worker) handleBatch(ctx, handlerCtx context.Context, c *Consumer) (int, bool, error) {
	batchID, found, err := nextBatch(ctx, w.db, c.queue, c.name)
	if err != nil || !found {
		return 0, false, err
	}
	msgs, err := batchMessages(ctx, w.db, batchID)
	if err != nil {
		return 0, true, fmt.Errorf("reading batch %d of %s: %w", batchID, c.queue, err)
	}
	results := runAll(handlerCtx, c, msgs)
	if handlerCtx.Err() != nil && slices.ContainsFunc(results, func(r result) bool { return r.err != nil }) {
		w.logger.Info("batch stopped; it returns to the queue",
			"queue", c.queue, "consumer", c.name, "batch_id", batchID, "messages", len(msgs))
		return len(msgs), true, nil
	}
	// The result must reach the database also after handlerCtx ends.
	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(handlerCtx), storeTimeout)
	defer cancel()
	if err := finishBatch(storeCtx, w.db, c.queue, batchID, results); err != nil {
		return len(msgs), true, fmt.Errorf("finishing batch %d of %s: %w", batchID, c.queue, err)
	}
	for _, r := range results {
		w.logResult(messageContext(handlerCtx, c, r.msg), r)
	}
	return len(msgs), true, nil
}

// runAll runs the handler of each message in order. After the end of
// ctx, it skips the rest of the batch.
func runAll(ctx context.Context, c *Consumer, msgs []Message) []result {
	results := make([]result, len(msgs))
	for i, msg := range msgs {
		r := &results[i]
		r.msg = msg
		if ctx.Err() != nil {
			r.err = ctx.Err()
			continue
		}
		start := time.Now()
		r.err = runHandler(messageContext(ctx, c, msg), c, msg)
		r.took = time.Since(start)
		if r.err != nil {
			r.delay = backoff(msg.Retries)
		}
	}
	return results
}

// messageContext adds the log attributes of msg to ctx.
func messageContext(ctx context.Context, c *Consumer, msg Message) context.Context {
	return logging.WithAttrs(ctx,
		slog.String("queue", c.queue),
		slog.String("consumer", c.name),
		slog.Int64("message_id", msg.ID),
		slog.String("message_type", msg.Type),
		slog.Int("message_retries", msg.Retries))
}

// runHandler runs the handler of msg with its timeout. A type without a
// handler is an error. A panic becomes an error, so the message retries
// and the worker continues.
func runHandler(ctx context.Context, c *Consumer, msg Message) (err error) {
	reg, ok := c.handlers[msg.Type]
	if !ok {
		return fmt.Errorf("no handler for the message type %q", msg.Type)
	}
	ctx, cancel := context.WithTimeout(ctx, reg.timeout)
	defer cancel()
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("handler panic: %v", p)
		}
	}()
	return reg.handler(ctx, msg)
}

// logResult logs the stored result of one message. It never logs the
// payload.
func (w *Worker) logResult(ctx context.Context, r result) {
	switch {
	case r.err == nil:
		w.logger.InfoContext(ctx, "message done", "duration", r.took)
	case r.dead:
		w.logger.ErrorContext(ctx, "message failed; it goes to the dead letter queue",
			"duration", r.took, "error", r.err)
	default:
		w.logger.WarnContext(ctx, "message failed; it will retry",
			"duration", r.took, "error", r.err, "retry_in", r.delay)
	}
}

// drain waits for the consumers. When the grace period ends first, it
// ends the handler contexts and waits a short time more.
func (w *Worker) drain(running *sync.WaitGroup, stopHandlers context.CancelFunc) {
	done := make(chan struct{})
	go func() {
		running.Wait()
		close(done)
	}()
	select {
	case <-done:
		return
	case <-time.After(w.grace):
	}
	stopHandlers()
	select {
	case <-done:
		w.logger.Info("worker ended the running handlers after the grace period")
	case <-time.After(shutdownHardLimit):
		w.logger.Error("a handler ignored the end of its context")
	}
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

// backoff returns the delay before the next delivery after retries
// failed deliveries: 15s, 30s, 1m, and so on up to 1h, plus up to 25%
// random jitter. The jitter spreads the retries of messages that failed
// together. PgQue stores the delay in whole seconds.
func backoff(retries int) time.Duration {
	d := min(15*time.Second<<min(max(retries, 0), 10), time.Hour)
	return d + rand.N(d/4)
}

// sleep waits for d or until ctx ends.
func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
