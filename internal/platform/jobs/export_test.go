package jobs

import "time"

// CleanupBatch is the row cap of one cleanup statement.
const CleanupBatch = cleanupBatch

// SetClock replaces the clock of the schedules, so a test can move to the
// next slot without a wait. The leases and the run times use the database
// clock.
func SetClock(w *Worker, now func() time.Time) {
	w.now = now
}

// SetShutdownGrace replaces the grace period that Run gives to the running
// jobs.
func SetShutdownGrace(w *Worker, grace time.Duration) {
	w.grace = grace
}
