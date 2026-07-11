// Package auth is the authentication layer: middleware that verifies
// credentials and stores the caller's Identity in the request context.
// Handlers and services never inspect Authorization headers themselves — they
// call FromContext.
//
// The template ships a single static API key. To move to real users
// (JWT/OIDC), replace RequireAPIKey with a middleware that verifies the token
// and fills Identity — the Identity-in-context contract stays the same, so
// nothing downstream changes.
package auth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
)

// Identity describes the authenticated caller.
type Identity struct {
	Subject string
}

type contextKey struct{}

// RequireAPIKey returns middleware that rejects requests whose bearer token
// does not match apiKey. It fails closed: an empty apiKey rejects everything.
func RequireAPIKey(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok || apiKey == "" || subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}` + "\n"))
				return
			}
			ctx := context.WithValue(r.Context(), contextKey{}, Identity{Subject: "api-key"})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// FromContext returns the Identity stored by the auth middleware.
func FromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(contextKey{}).(Identity)
	return identity, ok
}

func bearerToken(r *http.Request) (string, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		return "", false
	}
	return token, true
}
