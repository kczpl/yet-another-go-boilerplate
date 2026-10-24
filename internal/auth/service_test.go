package auth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kczpl/yet-another-go-boilerplate/internal/auth"
	"github.com/kczpl/yet-another-go-boilerplate/internal/testdb"
)

// seedUser inserts a user row directly, because auth tests must not depend
// on the user package.
func seedUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	// A random email lets one test seed many users.
	var id string
	err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, name, password_hash)
		VALUES (gen_random_uuid()::text || '@example.com', 'Auth Test', 'x')
		RETURNING id`).Scan(&id)
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

	if cookie.Name != "__Host-session_id" {
		t.Errorf("cookie name = %q, want __Host-session_id", cookie.Name)
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
	// A secure service reads only the __Host- cookie. A cookie without the
	// prefix can come from a subdomain or a plain HTTP page.
	plain := &http.Cookie{Name: "session_id", Value: cookie.Value}
	if _, err := svc.Identify(t.Context(), requestWith(plain)); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("Identify with an unprefixed cookie = %v, want ErrNoSession", err)
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
		{"short token", &http.Cookie{Name: "session_id", Value: "x"}},
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

func TestDeleteExpiredRemovesOnlyExpiredSessions(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	owner := seedUser(t, pool)
	// The expired rows belong to another user, so the session cap of
	// owner does not remove them. 1005 rows need two cleanup batches.
	other := seedUser(t, pool)
	_, err := pool.Exec(t.Context(), `INSERT INTO sessions (token_hash, user_id, expires_at) SELECT n::text, $1, now() - interval '1 hour' FROM generate_series(1, 1005) n`, other)
	if err != nil {
		t.Fatalf("seeding expired sessions: %v", err)
	}
	svc := auth.NewService(auth.NewRepo(pool), time.Hour, false)
	cookie := startSession(t, svc, owner)

	deleted, err := svc.DeleteExpired(t.Context())
	if err != nil || deleted != 1005 {
		t.Fatalf("DeleteExpired = %d, %v; want 1005, nil", deleted, err)
	}
	var expired int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions WHERE expires_at <= now()").Scan(&expired); err != nil {
		t.Fatalf("counting expired sessions: %v", err)
	}
	if expired != 0 {
		t.Errorf("expired rows = %d, want 0", expired)
	}
	if _, err := svc.Identify(t.Context(), requestWith(cookie)); err != nil {
		t.Fatalf("cleanup removed the live session: %v", err)
	}
}

func TestStartCapsSessionsPerUser(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	svc := auth.NewService(auth.NewRepo(pool), time.Hour, false)
	owner := seedUser(t, pool)
	other := seedUser(t, pool)
	otherCookie := startSession(t, svc, other)

	var cookies []*http.Cookie
	for range 12 {
		cookies = append(cookies, startSession(t, svc, owner))
	}

	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions WHERE user_id = $1", owner).Scan(&count); err != nil {
		t.Fatalf("counting sessions: %v", err)
	}
	if count != 10 {
		t.Fatalf("sessions = %d, want the cap of 10", count)
	}
	// The oldest sessions go first. The newest session always stays.
	for i, cookie := range cookies {
		_, err := svc.Identify(t.Context(), requestWith(cookie))
		if i < 2 && !errors.Is(err, auth.ErrNoSession) {
			t.Errorf("session %d = %v, want ErrNoSession (removed by the cap)", i, err)
		}
		if i >= 2 && err != nil {
			t.Errorf("session %d = %v, want valid", i, err)
		}
	}
	// The cap counts per user.
	if _, err := svc.Identify(t.Context(), requestWith(otherCookie)); err != nil {
		t.Errorf("session of another user = %v, want valid", err)
	}
}
