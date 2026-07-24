// Package notes is the example domain slice: domain types and errors (this
// file), business logic (service.go), and data access (repo.go). Copy the
// package for a new domain, then delete it.
package notes

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a note does not exist. The API layer maps it
// to a 404.
var ErrNotFound = errors.New("note not found")

// The db tags are the contract with pgx.RowToStructByName in repo.go: columns
// are matched by name, not position.
type Note struct {
	ID        uuid.UUID `db:"id"`
	Title     string    `db:"title"`
	Content   string    `db:"content"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// CreateParams carries already-validated input from the API layer.
type CreateParams struct {
	Title   string
	Content string
}

// UpdateParams is a partial update: nil fields keep their current value.
type UpdateParams struct {
	Title   *string
	Content *string
}

// ListParams is a limit/offset window over notes, newest first.
type ListParams struct {
	Limit  int
	Offset int
}
