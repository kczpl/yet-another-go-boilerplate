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

type Note struct {
	ID        uuid.UUID
	Title     string
	Content   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateParams carries already-validated input from the API layer.
type CreateParams struct {
	Title   string
	Content string
}

// ListParams is a limit/offset window over notes, newest first.
type ListParams struct {
	Limit  int
	Offset int
}
