-- A row counts the runs of a PgQue batch that did not finish, for example
-- because a handler crashed the worker process or ignored the end of its
-- context. PgQue counts a retry only when a consumer calls pgque.nack, so
-- such a batch comes back without a limit. internal/platform/queue adds 1
-- when a batch comes back open and deletes the row when the batch
-- finishes. After 3 runs, the worker skips the handlers and sends each
-- message of the batch to the retry queue.
CREATE TABLE queue_batch_runs (
    batch_id   bigint PRIMARY KEY,
    runs       integer NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
