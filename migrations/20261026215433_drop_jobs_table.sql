-- PgQue replaces the jobs table of the old queue. The next migration
-- installs PgQue. Stop the old workers before this migration runs: they
-- fail without the table. This migration deletes the jobs that are still
-- in the table.
--
-- The schedules of internal/platform/queue still use job_schedules. A row
-- keeps the last slot that a worker sent for a message type. The deleted
-- jobs can include the job of a current slot, so forget the old slots:
-- the new worker then sends the current slot again.
DROP TABLE jobs;
TRUNCATE job_schedules;
