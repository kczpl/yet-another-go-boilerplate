-- PgQue replaces the jobs table of the old queue. The next migration
-- installs PgQue. Stop the old workers when their queue is empty: this
-- migration deletes the jobs that are still in the table.
--
-- The schedules of internal/platform/queue still use job_schedules. A row
-- keeps the last slot that a worker sent for a message type.
DROP TABLE jobs;
