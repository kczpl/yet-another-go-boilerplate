package auth

import (
	"context"
	"time"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/jobs"
)

// deleteExpiredKind is the job kind of the session cleanup.
const deleteExpiredKind = "auth.delete_expired_sessions"

// Jobs registers the background jobs of the package on w. The session
// cleanup runs once every hour on one replica.
func Jobs(w *jobs.Worker, svc *Service) {
	w.Handle(deleteExpiredKind, time.Minute, handleDeleteExpired(svc))
	w.Schedule(deleteExpiredKind, time.Hour, 0)
}

// handleDeleteExpired removes the expired sessions. A second run of one
// slot finds no rows, so the handler is idempotent.
func handleDeleteExpired(svc *Service) jobs.Handler {
	return func(ctx context.Context, _ jobs.Job) error {
		_, err := svc.DeleteExpired(ctx)
		return err
	}
}
