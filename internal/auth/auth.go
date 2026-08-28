// Package auth owns browser sessions: the cookie, the identity, and the
// route guards. It knows users only as id strings and imports only
// platform, so every feature can import it without cycles.
package auth

import (
	"context"
	"errors"
	"time"
)

// ErrNoSession covers a missing cookie, an unknown token, and an expired
// token. Callers treat all three the same.
var ErrNoSession = errors.New("no valid session")

// Session is one row in the sessions table. The table stores only the
// SHA-256 hash of the token; the raw token exists only in the cookie.
type Session struct {
	TokenHash string    `db:"token_hash"`
	UserID    string    `db:"user_id"`
	CreatedAt time.Time `db:"created_at"`
	ExpiresAt time.Time `db:"expires_at"`
}

// Identity is the authenticated caller. LoadIdentity puts it in the
// request context. Features read it with IdentityFromContext.
type Identity struct {
	UserID string
}

type contextKey struct{}

// IdentityFromContext returns the identity that LoadIdentity attached.
// For anonymous requests, ok is false.
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(contextKey{}).(Identity)
	return id, ok
}
