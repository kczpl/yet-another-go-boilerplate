package web_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/logging"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/web"
)

func TestRateLimiterBurstThenInterval(t *testing.T) {
	t.Parallel()
	l := web.NewRateLimiter(10*time.Second, 3, "")
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	for i := range 3 {
		if _, ok := l.Allow("a", start); !ok {
			t.Fatalf("request %d of the burst was rejected", i+1)
		}
	}
	wait, ok := l.Allow("a", start)
	if ok || wait != 10*time.Second {
		t.Fatalf("request after the burst = (%v, %v), want (10s, false)", wait, ok)
	}
	// A rejected request does not use up capacity.
	if _, ok := l.Allow("a", start.Add(10*time.Second)); !ok {
		t.Fatal("request one interval later was rejected")
	}
	if _, ok := l.Allow("a", start.Add(10*time.Second)); ok {
		t.Fatal("second request in the same interval was allowed")
	}
	// Other keys have their own budget.
	if _, ok := l.Allow("b", start); !ok {
		t.Fatal("an unrelated key was rejected")
	}
	// After a long pause the full burst is available again.
	for i := range 3 {
		if _, ok := l.Allow("a", start.Add(time.Hour)); !ok {
			t.Fatalf("request %d after a pause was rejected", i+1)
		}
	}
}

func TestRateLimiterCapsKeys(t *testing.T) {
	t.Parallel()
	l := web.NewRateLimiter(time.Second, 1, "")
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// 100_000 is maxLimiterKeys in ratelimit.go.
	for i := range 100_000 {
		l.Allow(fmt.Sprint(i), start)
	}
	if _, ok := l.Allow("new", start); ok {
		t.Fatal("a new key was allowed in a full table")
	}
	// The sweep removes idle keys, so new clients get through again.
	if _, ok := l.Allow("new", start.Add(2*time.Minute)); !ok {
		t.Fatal("a new key was rejected after the sweep")
	}
}

func TestRateLimitResponse(t *testing.T) {
	t.Parallel()
	logger := logging.New(io.Discard, slog.LevelInfo, logging.JSON)
	l := web.NewRateLimiter(time.Minute, 1, "")
	h := web.E(logger, l.Limit(func(w http.ResponseWriter, _ *http.Request) error {
		_, _ = io.WriteString(w, "ok")
		return nil
	}))
	send := func(remoteAddr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		req.RemoteAddr = remoteAddr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := send("192.0.2.1:1000"); rec.Code != http.StatusOK {
		t.Fatalf("first request = %d, want 200", rec.Code)
	}
	rec := send("192.0.2.1:2000")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "60" {
		t.Errorf("Retry-After = %q, want 60", got)
	}
	if rec := send("192.0.2.2:1000"); rec.Code != http.StatusOK {
		t.Fatalf("other client = %d, want 200", rec.Code)
	}
	// One IPv6 client usually owns a full /64 network.
	if rec := send("[2001:db8::1]:1000"); rec.Code != http.StatusOK {
		t.Fatalf("IPv6 client = %d, want 200", rec.Code)
	}
	if rec := send("[2001:db8::2]:1000"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("same /64 network = %d, want 429", rec.Code)
	}
}

func TestRateLimitClientIPHeader(t *testing.T) {
	t.Parallel()
	logger := logging.New(io.Discard, slog.LevelInfo, logging.JSON)
	l := web.NewRateLimiter(time.Minute, 1, "X-Forwarded-For")
	h := web.E(logger, l.Limit(func(http.ResponseWriter, *http.Request) error { return nil }))
	send := func(forwardedFor string) int {
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		req.RemoteAddr = "10.0.0.1:1000" // the proxy
		if forwardedFor != "" {
			req.Header.Set("X-Forwarded-For", forwardedFor)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := send("203.0.113.9, 198.51.100.7"); code != http.StatusOK {
		t.Fatalf("first client = %d, want 200", code)
	}
	// The client controls the start of the list. Only the last address
	// counts, so a forged prefix does not give a new budget.
	if code := send("203.0.113.10, 198.51.100.7"); code != http.StatusTooManyRequests {
		t.Fatalf("forged prefix = %d, want 429", code)
	}
	if code := send("198.51.100.8"); code != http.StatusOK {
		t.Fatalf("second client = %d, want 200", code)
	}
	// Without the header, the request uses the key of the proxy.
	if code := send(""); code != http.StatusOK {
		t.Fatalf("request without the header = %d, want 200", code)
	}
	if code := send("not an address"); code != http.StatusTooManyRequests {
		t.Fatalf("bad header = %d, want 429 (the key of the proxy)", code)
	}
}
