package auth

import (
	"context"
	"time"
)

// Repository is the storage interface of the package. Define it here, next
// to its consumer (Service). postgres.go implements it.
type Repository interface {
	// Insert stores a session for userID that expires ttl after the
	// current database time. In the same transaction it deletes the
	// oldest sessions of the user, so at most keep sessions remain.
	Insert(ctx context.Context, userID, tokenHash string, ttl time.Duration, keep int) error
	// Get returns the unexpired session for tokenHash, or ErrNoSession.
	Get(ctx context.Context, tokenHash string) (Session, error)
	Delete(ctx context.Context, tokenHash string) error
	// DeleteExpired removes at most limit expired sessions and returns
	// the number of removed sessions.
	DeleteExpired(ctx context.Context, limit int) (int, error)
}
