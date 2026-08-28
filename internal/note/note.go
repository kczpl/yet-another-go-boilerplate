// Package note is the example feature. Copy this package to start a new
// feature.
//
// The file layout is fixed. This file keeps the types and the errors.
// service.go keeps the business rules. repository.go defines the storage
// interface. postgres.go holds the SQL. http.go registers the routes and
// the handlers. templates/ keeps the HTML.
package note

import (
	"errors"
	"time"
)

// ErrNotFound also covers a note that another user owns. Owner-scoped
// queries make foreign rows invisible, not forbidden.
var ErrNotFound = errors.New("note not found")

// ValidationError is rejected user input. Its text is safe to show to
// users, so handlers render it directly in forms.
type ValidationError string

func (e ValidationError) Error() string { return string(e) }

// Note is one row in the notes table.
type Note struct {
	ID        string    `db:"id"`
	UserID    string    `db:"user_id"`
	Text      string    `db:"text"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}
