// Package user owns accounts: credentials, login, and the /me profile.
// This file keeps the types and the errors. See package note for the file
// layout of a feature.
package user

import (
	"errors"
	"time"
)

var (
	ErrNotFound   = errors.New("user not found")
	ErrEmailTaken = errors.New("email already registered")
	// ErrInvalidCredentials covers both an unknown email and a wrong
	// password, so responses do not reveal which emails exist.
	ErrInvalidCredentials = errors.New("invalid email or password")
)

// ValidationError is rejected user input. Its text is safe to show to
// users, so handlers render it directly in forms.
type ValidationError string

func (e ValidationError) Error() string { return string(e) }

// User is one row in the users table. The ID is a plain string.
// PostgreSQL generates it (uuidv7).
type User struct {
	ID           string    `db:"id"`
	Email        string    `db:"email"`
	Name         string    `db:"name"`
	PasswordHash string    `db:"password_hash"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
}
