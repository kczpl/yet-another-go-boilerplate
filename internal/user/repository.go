package user

import "context"

// Repository is the storage interface of the feature. Define it here, next
// to its consumer (Service). postgres.go implements it.
type Repository interface {
	// Insert returns ErrEmailTaken if the email is already registered.
	Insert(ctx context.Context, email, name, passwordHash string) (User, error)
	// GetByID returns ErrNotFound for unknown ids.
	GetByID(ctx context.Context, id string) (User, error)
	// GetByEmail ignores the case of the email. It returns ErrNotFound for
	// unknown emails.
	GetByEmail(ctx context.Context, email string) (User, error)
	// Update returns ErrNotFound or ErrEmailTaken.
	Update(ctx context.Context, id, email, name string) (User, error)
}
