-- The jobs table is the background queue (internal/platform/jobs). A worker
-- claims a queued row with FOR UPDATE SKIP LOCKED, so each job goes to one
-- worker. A running row has a lease: locked_until. When the lease expires,
-- the cleanup puts the job back in the queue.
CREATE TABLE jobs (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    kind         text NOT NULL CHECK (kind ~ '^[a-z0-9_.]{1,100}$'),
    payload      jsonb NOT NULL DEFAULT '{}',
    status       text NOT NULL DEFAULT 'queued'
                 CHECK (status IN ('queued', 'running', 'done', 'failed')),
    run_at       timestamptz NOT NULL DEFAULT now(),
    attempts     integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts integer NOT NULL CHECK (max_attempts >= 1),
    locked_until timestamptz,
    last_error   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    -- Only a running job has a lease.
    CHECK ((status = 'running') = (locked_until IS NOT NULL))
);

-- The claim query reads the due queued jobs in run_at order.
CREATE INDEX jobs_queued_idx ON jobs (run_at, id) WHERE status = 'queued';
-- The cleanup finds the running jobs with an expired lease.
CREATE INDEX jobs_running_idx ON jobs (locked_until) WHERE status = 'running';
-- The cleanup removes the old finished jobs.
CREATE INDEX jobs_finished_idx ON jobs (updated_at) WHERE status IN ('done', 'failed');

-- A schedule row keeps the last slot that a worker enqueued for a kind.
-- The upsert lets only one replica enqueue each slot.
CREATE TABLE job_schedules (
    kind      text PRIMARY KEY,
    last_slot timestamptz NOT NULL
);
