package note

import "context"

// Repository is the storage interface of the feature. Define it here, next
// to its consumer (Service). postgres.go implements it.
type Repository interface {
	Insert(ctx context.Context, userID, text string) (Note, error)
	// ListByUser returns the user's notes, newest first, at most
	// listLimit rows.
	ListByUser(ctx context.Context, userID string) ([]Note, error)
	// Delete removes the note only if userID owns it. Otherwise it returns
	// ErrNotFound.
	Delete(ctx context.Context, id, userID string) error
}
