package queue

import "time"

// WorkerLockID is the session advisory lock of the active worker.
const WorkerLockID = workerLockID

// SetClock replaces the clock of the schedules, so a test can move to the
// next slot without a wait. The ticks and the retries use the database
// clock.
func SetClock(w *Worker, now func() time.Time) {
	w.now = now
}

// SetShutdownGrace replaces the grace period that Run gives to the running
// handlers.
func SetShutdownGrace(w *Worker, grace time.Duration) {
	w.grace = grace
}

// SetIntervals replaces the tick period of the active worker and the wait
// of a worker that did not get the worker lock.
func SetIntervals(w *Worker, tick, standby time.Duration) {
	w.tick = tick
	w.standby = standby
}
