package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"
)

// Browsers accept a __Host- cookie only with Secure, Path=/ and no Domain.
// The prefix stops a subdomain or a plain HTTP page from setting the
// session cookie. Development uses plain HTTP, so it cannot use the prefix.
const (
	secureCookieName = "__Host-session_id"
	devCookieName    = "session_id"
)

// maxSessionsPerUser caps the sessions of one user. A new login removes
// the oldest sessions above the cap, so a script that logs in again and
// again cannot fill the table.
const maxSessionsPerUser = 10

// Service issues, resolves, and revokes sessions.
type Service struct {
	repo Repository
	ttl  time.Duration
	// secure is off in development, so plain-http localhost works.
	secure     bool
	cookieName string
}

// NewService returns a session service. Pass secure = true outside
// development: the cookie then gets the Secure flag and the __Host- name.
func NewService(repo Repository, ttl time.Duration, secure bool) *Service {
	name := devCookieName
	if secure {
		name = secureCookieName
	}
	return &Service{repo: repo, ttl: ttl, secure: secure, cookieName: name}
}

// Start creates a session for userID and sets the cookie. The database
// gets only the hash of the token.
func (s *Service) Start(ctx context.Context, w http.ResponseWriter, userID string) error {
	token := newToken()
	if err := s.repo.Insert(ctx, userID, hashToken(token), s.ttl, maxSessionsPerUser); err != nil {
		return fmt.Errorf("storing session: %w", err)
	}
	// Remove at most 100 expired rows. A failure must not break the login.
	_ = s.repo.DeleteExpired(ctx)

	http.SetCookie(w, s.cookie(token, s.ttl))
	return nil
}

// Identify resolves the session cookie to an Identity. It returns
// ErrNoSession for anonymous or expired requests.
func (s *Service) Identify(ctx context.Context, r *http.Request) (Identity, error) {
	cookie, err := r.Cookie(s.cookieName)
	if err != nil {
		return Identity{}, ErrNoSession
	}
	session, err := s.repo.Get(ctx, hashToken(cookie.Value))
	if err != nil {
		return Identity{}, err
	}
	return Identity{UserID: session.UserID}, nil
}

// End revokes the session and clears the cookie. It is idempotent.
func (s *Service) End(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	if cookie, err := r.Cookie(s.cookieName); err == nil {
		if err := s.repo.Delete(ctx, hashToken(cookie.Value)); err != nil {
			return fmt.Errorf("deleting session: %w", err)
		}
	}
	http.SetCookie(w, s.cookie("", -1))
	return nil
}

func (s *Service) cookie(value string, ttl time.Duration) *http.Cookie {
	maxAge := -1
	if ttl > 0 {
		maxAge = int(ttl / time.Second)
	}
	return &http.Cookie{
		Name:     s.cookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// newToken returns 32 bytes of entropy as a 43-character URL-safe string.
func newToken() string {
	var b [32]byte
	rand.Read(b[:]) // crypto/rand.Read never returns an error (Go 1.24+).
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
