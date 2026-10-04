package web

import (
	"maps"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxLimiterKeys caps the memory of one RateLimiter. When the table is
// full, a new client gets 429 until the next sweep removes idle keys. A
// flood of new addresses can then block new clients, but it cannot use
// all memory.
const maxLimiterKeys = 100_000

// sweepInterval is the minimum time between two sweeps of idle keys.
const sweepInterval = time.Minute

// RateLimiter limits requests per client address with the generic cell
// rate algorithm (GCRA). Each key stores one time: the theoretical arrival
// time (TAT) of the next request. A client can send burst requests at once
// and after that one request per interval.
//
// The state is in process memory. Each replica counts on its own, so N
// replicas allow N times the rate. Use a shared store (PostgreSQL or the
// proxy) if that is not sufficient.
type RateLimiter struct {
	interval       time.Duration
	tolerance      time.Duration // interval * (burst - 1)
	clientIPHeader string

	mu        sync.Mutex
	tat       map[string]time.Time
	lastSweep time.Time
}

// NewRateLimiter returns a limiter that allows burst requests at once and
// after that one request per interval for each client. clientIPHeader
// names the header in which a trusted proxy sends the client address.
// Empty means the peer address of the connection.
func NewRateLimiter(interval time.Duration, burst int, clientIPHeader string) *RateLimiter {
	return &RateLimiter{
		interval:       interval,
		tolerance:      interval * time.Duration(max(burst, 1)-1),
		clientIPHeader: clientIPHeader,
		tat:            make(map[string]time.Time),
	}
}

// Limit wraps h. A client above the rate gets 429 with a Retry-After
// header, and h does not run.
func (l *RateLimiter) Limit(h HandlerE) HandlerE {
	return func(w http.ResponseWriter, r *http.Request) error {
		wait, ok := l.Allow(l.clientKey(r), time.Now())
		if !ok {
			// Round up, so the client does not come back too early.
			seconds := (wait + time.Second - 1) / time.Second
			w.Header().Set("Retry-After", strconv.Itoa(int(seconds)))
			return TooManyRequests("too many attempts, try again later")
		}
		return h(w, r)
	}
}

// Allow records one request for key at the time now. If key is above the
// rate, Allow returns ok = false and the wait until the next permitted
// request. A rejected request does not use up capacity.
func (l *RateLimiter) Allow(key string, now time.Time) (wait time.Duration, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)

	tat, found := l.tat[key]
	if !found && len(l.tat) >= maxLimiterKeys {
		return l.interval, false
	}
	if tat.Before(now) {
		tat = now
	}
	// earliest is the first time at which key can send again.
	if earliest := tat.Add(-l.tolerance); now.Before(earliest) {
		return earliest.Sub(now), false
	}
	l.tat[key] = tat.Add(l.interval)
	return 0, true
}

// sweep removes the keys whose TAT is not in the future. Such a key has
// its full burst again, so it is the same as a missing key.
func (l *RateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < sweepInterval {
		return
	}
	l.lastSweep = now
	maps.DeleteFunc(l.tat, func(_ string, tat time.Time) bool { return !tat.After(now) })
}

// clientKey returns the rate limit key of r: the client IP address. An
// IPv6 client usually controls a full /64 network, so the key of an IPv6
// address is its /64 prefix.
func (l *RateLimiter) clientKey(r *http.Request) string {
	addr := l.clientAddr(r).Unmap()
	if addr.Is6() {
		prefix, _ := addr.Prefix(64)
		return prefix.String()
	}
	return addr.String()
}

// clientAddr returns the client address of r. With clientIPHeader, it uses
// the last address in that header: the trusted proxy added it, so the
// client cannot forge it. If the header is missing or bad, clientAddr uses
// the peer address. All such requests then share the key of the proxy,
// which makes the limit stricter, not weaker.
func (l *RateLimiter) clientAddr(r *http.Request) netip.Addr {
	if l.clientIPHeader != "" {
		if addr, ok := lastAddr(r.Header.Values(l.clientIPHeader)); ok {
			return addr
		}
	}
	// net/http always sets RemoteAddr to ip:port. For any other value,
	// the zero Addr becomes one shared key.
	addrPort, _ := netip.ParseAddrPort(r.RemoteAddr)
	return addrPort.Addr()
}

// lastAddr parses the last address of a list header such as
// X-Forwarded-For. A proxy appends to the end of the list.
func lastAddr(values []string) (netip.Addr, bool) {
	if len(values) == 0 {
		return netip.Addr{}, false
	}
	list := values[len(values)-1]
	last := list[strings.LastIndexByte(list, ',')+1:]
	addr, err := netip.ParseAddr(strings.TrimSpace(last))
	return addr, err == nil
}
