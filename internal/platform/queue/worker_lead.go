package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// lead runs the active worker until ctx ends or the lock connection
// fails. It holds the lock until its handlers stopped, so another worker
// cannot start the same batch during the drain.
func (w *Worker) lead(ctx context.Context, lock *pgxpool.Conn) {
	defer unlock(lock)
	w.logger.Info("worker is active")

	// The handlers do not end with ctx. They get the grace period first.
	runCtx, stopRunning := context.WithCancel(ctx)
	defer stopRunning()
	handlerCtx, stopHandlers := context.WithCancel(context.WithoutCancel(ctx))
	defer stopHandlers()

	var running sync.WaitGroup
	running.Go(func() { w.maintainLoop(runCtx) })
	for _, c := range w.consumers {
		running.Go(func() { w.consume(runCtx, handlerCtx, c) })
	}
	lost := w.tickLoop(ctx, lock)
	stopRunning()
	if lost {
		// Another worker can be active now. Stop the handlers at once.
		w.logger.Error("worker lost the worker lock; it stops the running handlers")
		stopHandlers()
	}
	w.drain(&running, stopHandlers)
}

// tickLoop runs the ticker and sends the schedule slots until ctx ends.
// It returns true when the lock connection failed.
func (w *Worker) tickLoop(ctx context.Context, lock *pgxpool.Conn) bool {
	ticks := time.NewTicker(w.tick)
	defer ticks.Stop()
	slots := time.NewTicker(scheduleInterval)
	defer slots.Stop()

	w.poolTask(ctx, "sending scheduled messages", w.sendSchedules)
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticks.C:
			if !w.tickOn(ctx, lock) {
				return true
			}
		case <-slots.C:
			w.poolTask(ctx, "sending scheduled messages", w.sendSchedules)
		}
	}
}

// tickOn runs the PgQue ticker on the lock connection and wakes the
// consumers after a tick. The ticker uses the lock connection, so each
// tick also checks the lock. The query does not end with ctx: a cancel in
// the middle of a query closes the connection, and the lock with it. It
// returns false when the lock connection is closed.
func (w *Worker) tickOn(ctx context.Context, lock *pgxpool.Conn) bool {
	tickCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stepTimeout)
	defer cancel()
	n, err := tick(tickCtx, lock)
	if err != nil {
		w.logger.Error("ticking", "error", err)
		return !lock.Conn().IsClosed()
	}
	if n > 0 {
		// pgque.ticker() does not tell which queues got a tick.
		for _, c := range w.consumers {
			select {
			case c.wake <- struct{}{}:
			default:
			}
		}
	}
	return true
}

// maintainLoop runs the maintenance at once and then every 10 seconds,
// until ctx ends. It uses the pool, so a slow pass does not delay the
// ticks or put the lock at risk.
func (w *Worker) maintainLoop(ctx context.Context) {
	for ctx.Err() == nil {
		w.poolTask(ctx, "running the maintenance", w.maintain)
		sleep(ctx, maintenanceInterval, nil)
	}
}

// poolTask runs one task of the active worker on the pool and logs its
// error.
func (w *Worker) poolTask(ctx context.Context, msg string, task func(context.Context, querier) error) {
	taskCtx, cancel := context.WithTimeout(ctx, stepTimeout)
	defer cancel()
	w.logError(ctx, msg, task(taskCtx, w.db))
}

// maintain moves the due retries back to their queues, rotates the event
// tables, and deletes the old dead letters of the consumed queues and the
// old run counts.
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
	if err := purgeRuns(ctx, db, keepDead); err != nil {
		return fmt.Errorf("purging the batch runs: %w", err)
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

// drain waits for the consumers and the maintenance. When the grace
// period ends first, it ends the handler contexts and waits a short time
// more.
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
