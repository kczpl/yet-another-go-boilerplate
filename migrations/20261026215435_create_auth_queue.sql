-- The auth feature sends its background jobs to the auth queue
-- (internal/auth/jobs.go). The worker reads the queue as the consumer
-- "worker". Subscribe each consumer before the first message: a new
-- consumer does not see the older messages.
SELECT pgque.create_queue('auth');
SELECT pgque.subscribe('auth', 'worker');
