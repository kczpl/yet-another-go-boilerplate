package auth

import (
	"context"
	"time"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/queue"
)

const (
	// jobQueue is the PgQue queue of the package. The migration
	// create_auth_queue creates it and subscribes jobConsumer.
	jobQueue = "auth"
	// jobConsumer is the consumer name of the worker on jobQueue.
	jobConsumer = "worker"
	// deleteExpiredType is the message type of the session cleanup.
	deleteExpiredType = "auth.delete_expired_sessions"
)

// Jobs registers the background jobs of the package on w. The session
// cleanup runs once every hour.
func Jobs(w *queue.Worker, svc *Service) {
	c := w.Consumer(jobQueue, jobConsumer)
	c.Handle(deleteExpiredType, time.Minute, handleDeleteExpired(svc))
	w.Schedule(jobQueue, deleteExpiredType, time.Hour, 0)
}

// handleDeleteExpired removes the expired sessions. A second run of one
// slot finds no rows, so the handler is idempotent.
func handleDeleteExpired(svc *Service) queue.Handler {
	return func(ctx context.Context, _ queue.Message) error {
		_, err := svc.DeleteExpired(ctx)
		return err
	}
}
