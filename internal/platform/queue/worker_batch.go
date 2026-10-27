package queue

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/logging"
)

// batch is an open batch of a consumer.
type batch struct {
	id   int64
	msgs []Message
	// runs counts the earlier runs of the batch that did not finish.
	runs int
}

// result is the result of one message.
type result struct {
	msg   Message
	err   error
	took  time.Duration
	delay time.Duration
	// stopped marks a message that the worker stopped or skipped at the
	// end of the handler context.
	stopped bool
	// dead marks a message that finishBatch sent to the dead letter queue.
	dead bool
}

// consume runs the batches of c until ctx ends. The handlers use
// handlerCtx, which ends later than ctx.
func (w *Worker) consume(ctx, handlerCtx context.Context, c *Consumer) {
	for ctx.Err() == nil {
		_, found, err := w.handleBatch(ctx, handlerCtx, c)
		switch {
		case err != nil:
			w.logError(ctx, "handling a batch", err)
			sleep(ctx, errorDelay, nil)
		case !found:
			sleep(ctx, pollInterval, c.wake)
		}
	}
}

// handleBatch opens the next batch of c, runs the handlers, and finishes
// the batch. It opens the batch with ctx and runs the handlers with
// handlerCtx; a batch that ctx stops before the handlers stays open, and
// that is safe. It returns the number of messages, and false when no
// batch is ready.
//
// A batch that came back open too often, for example after crashes,
// does not run its handlers again: each message goes to the retry queue.
// When the worker stops a handler, the batch stays open, and the next
// batch call delivers it again.
func (w *Worker) handleBatch(ctx, handlerCtx context.Context, c *Consumer) (int, bool, error) {
	b, found, err := openBatch(ctx, w.db, c)
	if err != nil || !found {
		return 0, found, err
	}
	var results []result
	if b.runs >= maxBatchRuns {
		w.logger.Error("batch did not finish in earlier runs; its messages retry",
			"queue", c.queue, "consumer", c.name, "batch_id", b.id, "runs", b.runs, "messages", len(b.msgs))
		results = giveUp(b)
	} else {
		results = runAll(handlerCtx, c, b.msgs)
	}
	if slices.ContainsFunc(results, func(r result) bool { return r.stopped }) {
		w.leaveOpen(handlerCtx, c, b)
		return len(b.msgs), true, nil
	}
	// The result must reach the database also after handlerCtx ends.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(handlerCtx), finishTimeout)
	defer cancel()
	if err := finishBatch(finishCtx, w.db, c.queue, b.id, results); err != nil {
		return len(b.msgs), true, fmt.Errorf("finishing batch %d of %s: %w", b.id, c.queue, err)
	}
	for _, r := range results {
		w.logResult(messageContext(handlerCtx, c, r.msg), r)
	}
	return len(b.msgs), true, nil
}

// openBatch opens the next batch of c and reads its messages. When the
// batch was open before the call, an earlier run did not finish it:
// openBatch counts that run. It returns false when no batch is ready.
func openBatch(ctx context.Context, db *pgxpool.Pool, c *Consumer) (batch, bool, error) {
	open, wasOpen, err := currentBatch(ctx, db, c.queue, c.name)
	if err != nil {
		return batch{}, false, err
	}
	id, found, err := nextBatch(ctx, db, c.queue, c.name)
	if err != nil || !found {
		return batch{}, false, err
	}
	b := batch{id: id}
	if wasOpen && open == id {
		if b.runs, err = countRun(ctx, db, id); err != nil {
			return b, true, fmt.Errorf("counting the runs of batch %d of %s: %w", id, c.queue, err)
		}
	}
	if b.msgs, err = batchMessages(ctx, db, id); err != nil {
		return b, true, fmt.Errorf("reading batch %d of %s: %w", id, c.queue, err)
	}
	return b, true, nil
}

// runAll runs the handler of each message in order. After the end of
// ctx, it skips the rest of the batch.
func runAll(ctx context.Context, c *Consumer, msgs []Message) []result {
	results := make([]result, len(msgs))
	for i, msg := range msgs {
		r := &results[i]
		r.msg = msg
		if ctx.Err() != nil {
			r.err, r.stopped = ctx.Err(), true
			continue
		}
		start := time.Now()
		r.err = runHandler(messageContext(ctx, c, msg), c, msg)
		r.took = time.Since(start)
		if r.err != nil {
			// An error after the end of ctx comes from the stop, not from
			// the message.
			r.stopped = ctx.Err() != nil
			r.delay = backoff(msg.Retries)
		}
	}
	return results
}

// giveUp returns a retry for each message of b without a handler run.
func giveUp(b batch) []result {
	results := make([]result, len(b.msgs))
	for i, msg := range b.msgs {
		results[i] = result{
			msg:   msg,
			err:   fmt.Errorf("the batch did not finish in %d runs", b.runs),
			delay: backoff(msg.Retries),
		}
	}
	return results
}

// leaveOpen leaves a stopped batch open for the next batch call. The
// worker stopped the run, so the run does not count: leaveOpen takes back
// the count that the next open adds.
func (w *Worker) leaveOpen(ctx context.Context, c *Consumer, b batch) {
	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()
	if err := uncountRun(storeCtx, w.db, b.id); err != nil {
		w.logger.Error("storing a stopped batch", "queue", c.queue, "batch_id", b.id, "error", err)
	}
	w.logger.Info("batch stopped; it returns to the queue",
		"queue", c.queue, "consumer", c.name, "batch_id", b.id, "messages", len(b.msgs))
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

// backoff returns the delay before the next delivery after retries
// failed deliveries: 15s, 30s, 1m, and so on up to 1h, plus up to 25%
// random jitter. The jitter spreads the retries of messages that failed
// together. PgQue stores the delay in whole seconds.
func backoff(retries int) time.Duration {
	d := min(15*time.Second<<min(max(retries, 0), 10), time.Hour)
	return d + rand.N(d/4)
}
