package note

import "context"

// Repository is the storage interface of the feature. Define it here, next
// to its consumer (Service). postgres.go implements it. The parameters
// come in a fixed order: ctx, the owner id, then the resource id.
type Repository interface {
	Insert(ctx context.Context, userID, text string) (Note, error)
	// ListByUser returns at most limit notes of userID, newest first. A
	// non-zero after returns only the notes that come after that cursor.
	ListByUser(ctx context.Context, userID string, after Cursor, limit int) ([]Note, error)
	// Delete removes the note only if userID owns it. Otherwise it returns
	// ErrNotFound.
	Delete(ctx context.Context, userID, id string) error
}
