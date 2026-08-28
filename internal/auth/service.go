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

const cookieName = "session_id"

// Service issues, resolves, and revokes sessions.
type Service struct {
	repo Repository
	ttl  time.Duration
	// secure is off in development, so plain-http localhost works.
	secure bool
}

func NewService(repo Repository, ttl time.Duration, secure bool) *Service {
	return &Service{repo: repo, ttl: ttl, secure: secure}
}

// Start creates a session for userID and sets the cookie. The database
// gets only the hash of the token.
func (s *Service) Start(ctx context.Context, w http.ResponseWriter, userID string) error {
	token, err := newToken()
	if err != nil {
		return fmt.Errorf("generating session token: %w", err)
	}
	session := Session{
		TokenHash: hashToken(token),
		UserID:    userID,
		ExpiresAt: time.Now().Add(s.ttl),
	}
	if err := s.repo.Insert(ctx, session); err != nil {
		return fmt.Errorf("storing session: %w", err)
	}
	// Best-effort cleanup. A failure must not break the login.
	_ = s.repo.DeleteExpired(ctx)

	http.SetCookie(w, s.cookie(token, s.ttl))
	return nil
}

// Identify resolves the session cookie to an Identity. It returns
// ErrNoSession for anonymous or expired requests.
func (s *Service) Identify(ctx context.Context, r *http.Request) (Identity, error) {
	cookie, err := r.Cookie(cookieName)
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
	if cookie, err := r.Cookie(cookieName); err == nil {
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
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// newToken returns 32 bytes of entropy as a 43-character URL-safe string.
func newToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
