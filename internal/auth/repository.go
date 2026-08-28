package auth

import "context"

// Repository is the storage interface of the package. Define it here, next
// to its consumer (Service). postgres.go implements it.
type Repository interface {
	Insert(ctx context.Context, s Session) error
	// Get returns the unexpired session for tokenHash, or ErrNoSession.
	Get(ctx context.Context, tokenHash string) (Session, error)
	Delete(ctx context.Context, tokenHash string) error
	DeleteExpired(ctx context.Context) error
}
