package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rule pairs a fixed-window limit with its window. Auth endpoints use one rule
// per route; the same rule guards both the IP and the account bucket so a
// single config knob tunes each endpoint.
type Rule struct {
	Limit  int
	Window time.Duration
}

// Check defines one rate-limit dimension for a route: a key extractor plus the
// rule applied to that key.
type Check struct {
	Rule Rule
	Key  func(r *http.Request) (string, bool)
}

// bucket is the fixed-window state for a single key.
type bucket struct {
	windowStart time.Time
	window      time.Duration
	count       int
}

const janitorInterval = time.Minute

// maxAccountKeyBodyBytes bounds how much of a request body the account key
// may read; login/register bodies are tiny, and a body this large is rejected
// by the handler anyway.
const maxAccountKeyBodyBytes = 4 << 10

// Limiter is a fixed-window, keyed rate limiter safe for concurrent use.
//
// It is intentionally in-memory and single-instance: the API runs as one
// process, so shared local state is sufficient. If the deployment topology
// changes (multiple replicas), replace this store with a shared one (e.g.
// Redis) — the middleware callers remain unchanged.
type Limiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	now      func() time.Time
	stop     chan struct{}
	stopOnce sync.Once
}

// NewLimiter creates a limiter and starts its janitor goroutine so expired
// buckets are pruned and memory does not grow without bound.
func NewLimiter() *Limiter {
	l := &Limiter{
		buckets: make(map[string]*bucket),
		now:     time.Now,
		stop:    make(chan struct{}),
	}
	go l.janitor()
	return l
}

// Stop halts the janitor goroutine. Allow may still be called afterwards;
// expired buckets are then reclaimed lazily on access.
func (l *Limiter) Stop() {
	l.stopOnce.Do(func() { close(l.stop) })
}

func (l *Limiter) janitor() {
	ticker := time.NewTicker(janitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			l.prune()
		}
	}
}

// Allow records one request for key and reports whether it is within limit for
// the fixed window. When denied it returns how long to wait (Retry-After).
func (l *Limiter) Allow(key string, limit int, window time.Duration) (bool, time.Duration) {
	if limit <= 0 {
		return false, positive(window)
	}
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &bucket{windowStart: now, window: window, count: 1}
		return true, 0
	}
	if !now.Before(b.windowStart.Add(b.window)) {
		b.windowStart = now
		b.window = window
		b.count = 1
		return true, 0
	}
	if b.count < limit {
		b.count++
		return true, 0
	}
	return false, positive(b.windowStart.Add(b.window).Sub(now))
}

// prune drops buckets whose window has fully elapsed. Called periodically by
// the janitor; exposed separately so tests exercise it deterministically.
func (l *Limiter) prune() {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	for key, b := range l.buckets {
		if !now.Before(b.windowStart.Add(b.window)) {
			delete(l.buckets, key)
		}
	}
}

func positive(d time.Duration) time.Duration {
	if d <= 0 {
		return time.Second
	}
	return d
}

// RateLimit wraps a handler so each check's key is bucketed separately. Any
// denying check yields a generic 429 with Retry-After; the response never
// reveals whether a particular account exists.
func RateLimit(l *Limiter, checks ...Check) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var retry time.Duration
			denied := false
			for _, c := range checks {
				key, ok := c.Key(r)
				if !ok {
					continue
				}
				allowed, after := l.Allow(key, c.Rule.Limit, c.Rule.Window)
				if !allowed {
					denied = true
					if after > retry {
						retry = after
					}
				}
			}
			if denied {
				writeTooManyRequests(w, retry)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP returns the client IP for rate limiting: the first X-Forwarded-For
// value if present, else the remote address with its port stripped. Consistent
// with the rest of the API (see handlers.clientMeta).
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
			return first
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// IPKey keys the IP rate-limit bucket for a request.
func IPKey(r *http.Request) (string, bool) {
	return "ip:" + ClientIP(r), true
}

// AccountKey keys the account rate-limit bucket from the normalized body email
// (lower-cased, trimmed). The body is read and restored so the handler can
// still decode it. Empty or malformed bodies fall through to the IP bucket.
func AccountKey(r *http.Request) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(emailFromBody(r)))
	if email == "" {
		return "", false
	}
	return "email:" + email, true
}

func emailFromBody(r *http.Request) string {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxAccountKeyBodyBytes+1))
	if err != nil {
		return ""
	}
	// Restore the body for the handler regardless of parse success.
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(body) > maxAccountKeyBodyBytes {
		return "" // oversized body: the handler rejects it anyway
	}
	var payload struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return payload.Email
}

func writeTooManyRequests(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int64(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"error":{"code":"too_many_requests","message":"Too many requests. Please try again later."}}`))
}
