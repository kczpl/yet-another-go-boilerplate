package auth_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kczpl/yet-another-go-boilerplate/internal/auth"
	"github.com/kczpl/yet-another-go-boilerplate/internal/testdb"
)

// seedUser inserts a user row directly, because auth tests must not depend
// on the user package.
func seedUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	email := fmt.Sprintf("auth-%d@example.com", time.Now().UnixNano())
	rows, _ := pool.Query(t.Context(),
		"INSERT INTO users (email, name, password_hash) VALUES ($1, 'Auth Test', 'x') RETURNING id",
		email)
	id, err := pgx.CollectExactlyOneRow(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("seeding user: %v", err)
	}
	return id
}

// startSession runs Start and returns the session cookie that Start set.
func startSession(t *testing.T, svc *auth.Service, userID string) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := svc.Start(t.Context(), rec, userID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Start set %d cookies, want 1", len(cookies))
	}
	return cookies[0]
}

func requestWith(cookie *http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return r
}

func TestSessionLifecycle(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	svc := auth.NewService(auth.NewRepo(pool), time.Hour, true)
	userID := seedUser(t, pool)

	cookie := startSession(t, svc, userID)

	if cookie.Name != "session_id" {
		t.Errorf("cookie name = %q, want session_id", cookie.Name)
	}
	if len(cookie.Value) != 43 {
		t.Errorf("token length = %d, want 43 (32 bytes base64url)", len(cookie.Value))
	}
	if !cookie.HttpOnly {
		t.Error("cookie must be HttpOnly")
	}
	if !cookie.Secure {
		t.Error("cookie must be Secure when the service is constructed secure")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
	}

	identity, err := svc.Identify(t.Context(), requestWith(cookie))
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if identity.UserID != userID {
		t.Errorf("UserID = %q, want %q", identity.UserID, userID)
	}

	// End revokes the session and clears the cookie.
	rec := httptest.NewRecorder()
	if err := svc.End(t.Context(), rec, requestWith(cookie)); err != nil {
		t.Fatalf("End: %v", err)
	}
	cleared := rec.Result().Cookies()
	if len(cleared) != 1 || cleared[0].MaxAge >= 0 {
		t.Errorf("End cookies = %+v, want one cookie with MaxAge < 0", cleared)
	}
	if _, err := svc.Identify(t.Context(), requestWith(cookie)); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("Identify after End = %v, want ErrNoSession", err)
	}

	// End is idempotent.
	if err := svc.End(t.Context(), httptest.NewRecorder(), requestWith(cookie)); err != nil {
		t.Errorf("second End: %v", err)
	}
}

func TestIdentifyRejectsBadTokens(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	svc := auth.NewService(auth.NewRepo(pool), time.Hour, false)
	userID := seedUser(t, pool)
	startSession(t, svc, userID)

	tests := []struct {
		name   string
		cookie *http.Cookie
	}{
		{"no cookie", nil},
		{"unknown token", &http.Cookie{Name: "session_id", Value: "e30gPGZha2UgdG9rZW4sIDMyIGJ5dGVzPiAK"}},
		{"empty token", &http.Cookie{Name: "session_id", Value: "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Identify(t.Context(), requestWith(tt.cookie))
			if !errors.Is(err, auth.ErrNoSession) {
				t.Errorf("Identify = %v, want ErrNoSession", err)
			}
		})
	}
}

func TestIdentifyRejectsExpiredSession(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	userID := seedUser(t, pool)

	// A service with a negative TTL writes sessions that are already
	// expired.
	expired := auth.NewService(auth.NewRepo(pool), -time.Minute, false)
	cookie := startSession(t, expired, userID)

	live := auth.NewService(auth.NewRepo(pool), time.Hour, false)
	if _, err := live.Identify(t.Context(), requestWith(cookie)); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("Identify with expired session = %v, want ErrNoSession", err)
	}
}

func TestStartCleansUpExpiredSessions(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	userID := seedUser(t, pool)

	expired := auth.NewService(auth.NewRepo(pool), -time.Minute, false)
	startSession(t, expired, userID)

	// Each later Start call also deletes the expired rows.
	live := auth.NewService(auth.NewRepo(pool), time.Hour, false)
	startSession(t, live, userID)

	var count int
	err := pool.QueryRow(t.Context(),
		"SELECT count(*) FROM sessions WHERE expires_at <= now()").Scan(&count)
	if err != nil {
		t.Fatalf("counting expired sessions: %v", err)
	}
	if count != 0 {
		t.Errorf("expired sessions left = %d, want 0", count)
	}
}
