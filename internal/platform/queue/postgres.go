package queue

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// workerLockID is the session advisory lock of the active worker. The
	// value is arbitrary but must be unique in the database.
	workerLockID = 776_012_346
	// defaultMaxRetries is the retry limit of a queue without the
	// max_retries setting. pgque.nack uses the same default.
	defaultMaxRetries = 5
	// maxRetryCalls caps the calls of pgque.maint_retry_events in one
	// maintenance pass. One call moves at most 10 messages.
	maxRetryCalls = 100
)

// querier runs SQL on a pool, on a pool connection, or in a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// send adds one message. The jsonb cast makes PostgreSQL check the
// payload.
func send(ctx context.Context, db DB, queue, typ string, payload []byte) (int64, error) {
	var id int64
	err := db.QueryRow(ctx, `SELECT pgque.send(@queue, @type, @payload::jsonb)`,
		pgx.NamedArgs{"queue": queue, "type": typ, "payload": string(payload)}).Scan(&id)
	return id, err
}

// subscribed reports whether consumer has a subscription on queue.
func subscribed(ctx context.Context, db querier, queue, consumer string) (bool, error) {
	var ok bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM pgque.get_consumer_info(@queue, @consumer))`,
		pgx.NamedArgs{"queue": queue, "consumer": consumer}).Scan(&ok)
	return ok, err
}

// nextBatch opens the next batch of consumer and returns its id. A batch
// that the consumer did not finish comes back again. It returns false
// when no batch is ready. A batch can be empty.
func nextBatch(ctx context.Context, db querier, queue, consumer string) (int64, bool, error) {
	var id *int64
	err := db.QueryRow(ctx, `SELECT pgque.next_batch(@queue, @consumer)`,
		pgx.NamedArgs{"queue": queue, "consumer": consumer}).Scan(&id)
	if err != nil || id == nil {
		return 0, false, err
	}
	return *id, true, nil
}

// batchMessages reads all messages of a batch. PgQue allows a NULL type
// and a NULL payload; they become empty values.
func batchMessages(ctx context.Context, db querier, batchID int64) ([]Message, error) {
	rows, _ := db.Query(ctx, `
		SELECT ev_id, coalesce(ev_type, ''), coalesce(ev_data, ''), coalesce(ev_retry, 0), ev_time
		FROM pgque.get_batch_events(@batch_id)
		ORDER BY ev_id`,
		pgx.NamedArgs{"batch_id": batchID})
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Message, error) {
		var (
			m       Message
			payload string
		)
		err := row.Scan(&m.ID, &m.Type, &payload, &m.Retries, &m.CreatedAt)
		m.Payload = []byte(payload)
		return m, err
	})
}

// finishBatch stores the results of a batch in one transaction. A failed
// message goes to the retry queue or to the dead letter queue. Then the
// batch closes, and the consumer moves past it. A rollback leaves the
// batch open, so the next batch call delivers it again. finishBatch sets
// the dead field of each failed result.
func finishBatch(ctx context.Context, db *pgxpool.Pool, queue string, batchID int64, results []result) error {
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if err := storeFailures(ctx, tx, queue, batchID, results); err != nil {
			return err
		}
		var finished int
		err := tx.QueryRow(ctx, `SELECT pgque.ack(@batch_id)`,
			pgx.NamedArgs{"batch_id": batchID}).Scan(&finished)
		if err == nil && finished != 1 {
			err = errors.New("the batch is not open")
		}
		return err
	})
}

// storeFailures sends each failed message to the retry queue or to the
// dead letter queue.
func storeFailures(ctx context.Context, tx pgx.Tx, queue string, batchID int64, results []result) error {
	if !slices.ContainsFunc(results, func(r result) bool { return r.err != nil }) {
		return nil
	}
	var maxRetries int
	if err := tx.QueryRow(ctx, `
		SELECT coalesce(queue_max_retries, @default) FROM pgque.queue WHERE queue_name = @queue`,
		pgx.NamedArgs{"queue": queue, "default": defaultMaxRetries}).Scan(&maxRetries); err != nil {
		return err
	}
	for i := range results {
		if err := storeFailure(ctx, tx, batchID, &results[i], maxRetries); err != nil {
			return err
		}
	}
	return nil
}

// storeFailure stores one failed message and sets r.dead. It ignores a
// message without an error.
func storeFailure(ctx context.Context, tx pgx.Tx, batchID int64, r *result, maxRetries int) error {
	switch {
	case r.err == nil:
		return nil
	case isPermanent(r.err):
		r.dead = true
		return deadLetter(ctx, tx, batchID, r.msg.ID, errorText(r.err))
	default:
		// pgque.nack makes the same decision.
		r.dead = r.msg.Retries >= maxRetries
		return nack(ctx, tx, batchID, r.msg.ID, r.delay, errorText(r.err))
	}
}

// nack sends a message to the retry queue, or to the dead letter queue
// when the message has no retries left. pgque.nack reads only the id from
// the message row.
func nack(ctx context.Context, tx pgx.Tx, batchID, msgID int64, delay time.Duration, reason string) error {
	_, err := tx.Exec(ctx, `
		SELECT pgque.nack(
			@batch_id,
			ROW(@msg_id::bigint, @batch_id::bigint, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL)::pgque.message,
			@delay::interval,
			@reason)`,
		pgx.NamedArgs{"batch_id": batchID, "msg_id": msgID, "delay": delay, "reason": reason})
	return err
}

// deadLetter sends a message to the dead letter queue at once. It copies
// the stored message, as pgque.nack does.
func deadLetter(ctx context.Context, tx pgx.Tx, batchID, msgID int64, reason string) error {
	var n int
	return tx.QueryRow(ctx, `
		SELECT pgque.event_dead(@batch_id, ev_id, @reason, ev_time, ev_txid::text::xid8, ev_retry,
			ev_type, ev_data, ev_extra1, ev_extra2, ev_extra3, ev_extra4)
		FROM pgque.get_batch_events(@batch_id)
		WHERE ev_id = @msg_id`,
		pgx.NamedArgs{"batch_id": batchID, "msg_id": msgID, "reason": reason}).Scan(&n)
}

// tick runs the PgQue ticker for all queues. A tick closes a batch: the
// consumers see the messages that committed before it. It returns the
// number of queues with a new tick.
func tick(ctx context.Context, db querier) (int64, error) {
	var n int64
	err := db.QueryRow(ctx, `SELECT pgque.ticker()`).Scan(&n)
	return n, err
}

// forceTick makes the next tick of queue happen at once. Without it, the
// ticker waits for more messages or for the tick lag of the queue (3
// seconds by default).
func forceTick(ctx context.Context, db querier, queue string) error {
	_, err := db.Exec(ctx, `SELECT pgque.force_next_tick(@queue)`, pgx.NamedArgs{"queue": queue})
	return err
}

// moveRetries moves the due retries back to their queues. Each call of
// pgque.maint_retry_events is a separate transaction, and the next tick
// must come after it.
func moveRetries(ctx context.Context, db querier) error {
	for range maxRetryCalls {
		var moved int
		if err := db.QueryRow(ctx, `SELECT pgque.maint_retry_events()`).Scan(&moved); err != nil {
			return err
		}
		if moved == 0 {
			return nil
		}
	}
	return nil
}

// rotate runs the table rotation of PgQue: an old event table is
// truncated when all consumers are past it. Step 2 must run in a separate
// transaction after step 1.
func rotate(ctx context.Context, db querier) error {
	if _, err := db.Exec(ctx, `SELECT pgque.maint()`); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `SELECT pgque.maint_rotate_tables_step2()`)
	return err
}

// purgeDead deletes the dead letters of queue that are older than keep.
func purgeDead(ctx context.Context, db querier, queue string, keep time.Duration) error {
	_, err := db.Exec(ctx, `SELECT pgque.dlq_purge(@queue, @keep::interval)`,
		pgx.NamedArgs{"queue": queue, "keep": keep})
	return err
}

// sendSlot sends one message for a schedule slot. The upsert on
// job_schedules lets only the first worker send the slot, also across a
// change of the active worker. The upsert and the send are one statement,
// so they commit together. It returns true when it sent a message.
func sendSlot(ctx context.Context, db querier, queue, typ string, slot time.Time, payload []byte) (bool, error) {
	rows, _ := db.Query(ctx, `
		WITH claimed AS (
			INSERT INTO job_schedules (kind, last_slot) VALUES (@type, @slot::timestamptz)
			ON CONFLICT (kind) DO UPDATE SET last_slot = excluded.last_slot
			WHERE job_schedules.last_slot < excluded.last_slot
			RETURNING kind
		)
		SELECT pgque.send(@queue, kind, @payload::jsonb) FROM claimed`,
		pgx.NamedArgs{"queue": queue, "type": typ, "slot": slot, "payload": string(payload)})
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	return len(ids) == 1, err
}

// tryLock takes the worker lock on a dedicated pool connection. It
// returns nil when another worker holds the lock. The lock lives as long
// as the session, so the caller must keep the connection and give it to
// unlock.
func tryLock(ctx context.Context, pool *pgxpool.Pool) (*pgxpool.Conn, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	var locked bool
	err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, workerLockID).Scan(&locked)
	switch {
	case err != nil:
		// The server can grant the lock before the error. A closed
		// session holds no lock.
		closeConn(conn)
		return nil, err
	case !locked:
		conn.Release()
		return nil, nil
	}
	return conn, nil
}

// unlock removes the worker lock and returns the connection to the pool.
// If the unlock fails, it closes the connection: the server then drops
// every lock of the session.
func unlock(conn *pgxpool.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	var unlocked bool
	err := conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1)`, workerLockID).Scan(&unlocked)
	if err != nil || !unlocked {
		closeConn(conn)
		return
	}
	conn.Release()
}

// closeConn closes the connection and gives it back to the pool, which
// then drops it.
func closeConn(conn *pgxpool.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	_ = conn.Conn().Close(ctx)
	conn.Release()
}
