package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// cleanupBatch caps the rows of one cleanup statement, so one statement
// stays short and holds few locks.
const cleanupBatch = 1000

// insertJob adds a job. A zero runAt means the current database time.
func insertJob(ctx context.Context, db DB, kind string, payload []byte, runAt time.Time, maxAttempts int) (string, error) {
	var at *time.Time
	if !runAt.IsZero() {
		at = &runAt
	}
	var id string
	err := db.QueryRow(ctx, `
		INSERT INTO jobs (kind, payload, run_at, max_attempts)
		VALUES (@kind, @payload::jsonb, COALESCE(@run_at::timestamptz, now()), @max_attempts)
		RETURNING id`,
		pgx.NamedArgs{
			"kind":         kind,
			"payload":      string(payload),
			"run_at":       at,
			"max_attempts": maxAttempts,
		}).Scan(&id)
	return id, err
}

// claim takes the oldest due job of one of kinds and marks it running.
// leaseMS[i] is the lease of kinds[i] in milliseconds. SKIP LOCKED skips
// the rows that another worker claims at the same time. It returns false
// when no job is due.
func claim(ctx context.Context, db *pgxpool.Pool, kinds []string, leaseMS []int64) (Job, bool, error) {
	var job Job
	err := db.QueryRow(ctx, `
		UPDATE jobs SET
			status = 'running',
			attempts = attempts + 1,
			locked_until = now() + (@lease_ms::bigint[])[array_position(@kinds::text[], kind)] * interval '1 millisecond',
			updated_at = now()
		WHERE id = (
			SELECT id FROM jobs
			WHERE status = 'queued' AND run_at <= now() AND kind = ANY(@kinds::text[])
			ORDER BY run_at, id
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, kind, payload, attempts, max_attempts`,
		pgx.NamedArgs{"kinds": kinds, "lease_ms": leaseMS},
	).Scan(&job.ID, &job.Kind, &job.Payload, &job.Attempt, &job.maxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	return job, true, nil
}

// The mark functions and release store the result of one attempt. The
// attempt number is a fencing token: a worker that lost its lease changes
// no row, because the cleanup or another worker changed the status or the
// attempts. They return false when no row changed.

func markDone(ctx context.Context, db *pgxpool.Pool, job Job) (bool, error) {
	tag, err := db.Exec(ctx, `
		UPDATE jobs SET status = 'done', locked_until = NULL, last_error = NULL, updated_at = now()
		WHERE id = @id AND status = 'running' AND attempts = @attempt`,
		pgx.NamedArgs{"id": job.ID, "attempt": job.Attempt})
	return tag.RowsAffected() == 1, err
}

func markRetry(ctx context.Context, db *pgxpool.Pool, job Job, delay time.Duration, lastError string) (bool, error) {
	tag, err := db.Exec(ctx, `
		UPDATE jobs SET
			status = 'queued',
			run_at = now() + @delay::interval,
			locked_until = NULL,
			last_error = @last_error,
			updated_at = now()
		WHERE id = @id AND status = 'running' AND attempts = @attempt`,
		pgx.NamedArgs{"id": job.ID, "attempt": job.Attempt, "delay": delay, "last_error": lastError})
	return tag.RowsAffected() == 1, err
}

func markFailed(ctx context.Context, db *pgxpool.Pool, job Job, lastError string) (bool, error) {
	tag, err := db.Exec(ctx, `
		UPDATE jobs SET status = 'failed', locked_until = NULL, last_error = @last_error, updated_at = now()
		WHERE id = @id AND status = 'running' AND attempts = @attempt`,
		pgx.NamedArgs{"id": job.ID, "attempt": job.Attempt, "last_error": lastError})
	return tag.RowsAffected() == 1, err
}

// release returns a job that the worker stopped to the queue. The job did
// not fail, so release takes back the attempt. The fence still holds: the
// attempt number goes back to its value before this claim, and a worker
// that lost its lease holds that value or a smaller one. The next claim
// adds 1 again.
func release(ctx context.Context, db *pgxpool.Pool, job Job) (bool, error) {
	tag, err := db.Exec(ctx, `
		UPDATE jobs SET
			status = 'queued',
			attempts = attempts - 1,
			run_at = now(),
			locked_until = NULL,
			last_error = 'the worker stopped',
			updated_at = now()
		WHERE id = @id AND status = 'running' AND attempts = @attempt`,
		pgx.NamedArgs{"id": job.ID, "attempt": job.Attempt})
	return tag.RowsAffected() == 1, err
}

// recoverExpired returns the running jobs with an expired lease to the
// queue. A job without attempts left fails. It returns the number of
// jobs that it changed.
func recoverExpired(ctx context.Context, db *pgxpool.Pool) (int, error) {
	return inBatches(func() (int64, error) {
		tag, err := db.Exec(ctx, `
			WITH expired AS MATERIALIZED (
				SELECT id FROM jobs
				WHERE status = 'running' AND locked_until < now()
				LIMIT @batch
				FOR UPDATE SKIP LOCKED
			)
			UPDATE jobs SET
				status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'queued' END,
				run_at = now(),
				locked_until = NULL,
				last_error = 'the lease expired',
				updated_at = now()
			WHERE id IN (SELECT id FROM expired)`,
			pgx.NamedArgs{"batch": cleanupBatch})
		return tag.RowsAffected(), err
	})
}

// prune deletes the done jobs older than keepDone and the failed jobs
// older than keepFailed. It returns the number of deleted jobs.
func prune(ctx context.Context, db *pgxpool.Pool, keepDone, keepFailed time.Duration) (int, error) {
	return inBatches(func() (int64, error) {
		tag, err := db.Exec(ctx, `
			WITH old AS MATERIALIZED (
				SELECT id FROM jobs
				WHERE (status = 'done' AND updated_at < now() - @keep_done::interval)
				   OR (status = 'failed' AND updated_at < now() - @keep_failed::interval)
				LIMIT @batch
				FOR UPDATE SKIP LOCKED
			)
			DELETE FROM jobs WHERE id IN (SELECT id FROM old)`,
			pgx.NamedArgs{"keep_done": keepDone, "keep_failed": keepFailed, "batch": cleanupBatch})
		return tag.RowsAffected(), err
	})
}

// inBatches runs one batch statement until a batch is not full.
func inBatches(batch func() (int64, error)) (int, error) {
	total := 0
	for {
		n, err := batch()
		total += int(n)
		if err != nil || n < cleanupBatch {
			return total, err
		}
	}
}

// enqueueSlot enqueues one job for a schedule slot. The upsert on
// job_schedules lets only the first worker enqueue the slot. The slot is
// skipped when a job of the kind is still queued or running, so two runs
// of one schedule never overlap. The job is due at once: the slot comes
// from the clock of the worker, and that clock can be ahead of the
// database clock. It returns true when it added a job.
func enqueueSlot(ctx context.Context, db *pgxpool.Pool, kind string, slot time.Time, payload []byte, maxAttempts int) (bool, error) {
	tag, err := db.Exec(ctx, `
		WITH claimed AS (
			INSERT INTO job_schedules (kind, last_slot) VALUES (@kind, @slot::timestamptz)
			ON CONFLICT (kind) DO UPDATE SET last_slot = excluded.last_slot
			WHERE job_schedules.last_slot < excluded.last_slot
			RETURNING kind
		)
		INSERT INTO jobs (kind, payload, run_at, max_attempts)
		SELECT kind, @payload::jsonb, LEAST(@slot::timestamptz, now()), @max_attempts::integer FROM claimed
		WHERE NOT EXISTS (
			SELECT 1 FROM jobs WHERE kind = @kind AND status IN ('queued', 'running')
		)`,
		pgx.NamedArgs{"kind": kind, "slot": slot, "payload": string(payload), "max_attempts": maxAttempts})
	return tag.RowsAffected() == 1, err
}
