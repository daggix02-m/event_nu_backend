package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock returns a clock whose value advances by an externally controlled
// delta, so tests can walk time without sleeping.
func fakeClock(start time.Time) (func() time.Time, *time.Duration) {
	delta := new(time.Duration)
	return func() time.Time { return start.Add(*delta) }, delta
}

func testLimiter(now func() time.Time) *Limiter {
	return &Limiter{buckets: make(map[string]*bucket), now: now}
}

func TestAllowBlocksAtExactlyLimitPlusOne(t *testing.T) {
	now, _ := fakeClock(time.Unix(1_700_000_000, 0))
	l := testLimiter(now)

	const limit = 3
	allowed := 0
	blocked := 0
	for i := 0; i < limit+2; i++ {
		ok, _ := l.Allow("k", limit, time.Minute)
		if ok {
			allowed++
		} else {
			blocked++
		}
	}
	if allowed != limit {
		t.Fatalf("expected %d allowed, got %d", limit, allowed)
	}
	if blocked != 2 {
		t.Fatalf("expected 2 blocked (limit+1 and beyond), got %d", blocked)
	}
}

func TestAllowWindowRolloverResetsCount(t *testing.T) {
	now, advance := fakeClock(time.Unix(1_700_000_000, 0))
	l := testLimiter(now)

	const limit = 2
	for i := 0; i < limit; i++ {
		if ok, _ := l.Allow("k", limit, time.Minute); !ok {
			t.Fatalf("request %d should be allowed inside the window", i+1)
		}
	}
	if ok, _ := l.Allow("k", limit, time.Minute); ok {
		t.Fatal("expected denial once the limit is exhausted")
	}

	// Cross the window boundary: the count must reset and the key is usable again.
	*advance = time.Minute + time.Nanosecond
	for i := 0; i < limit; i++ {
		if ok, _ := l.Allow("k", limit, time.Minute); !ok {
			t.Fatalf("request %d after rollover should be allowed", i+1)
		}
	}
}

func TestAllowKeysAreIndependent(t *testing.T) {
	now, _ := fakeClock(time.Unix(1_700_000_000, 0))
	l := testLimiter(now)

	if ok, _ := l.Allow("a", 1, time.Minute); !ok {
		t.Fatal("first allow for key a must succeed")
	}
	if ok, _ := l.Allow("a", 1, time.Minute); ok {
		t.Fatal("key a must be exhausted at its limit")
	}
	if ok, _ := l.Allow("b", 1, time.Minute); !ok {
		t.Fatal("key b must be independent of key a")
	}
}

func TestAllowRetryAfterMath(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	now, advance := fakeClock(start)
	l := testLimiter(now)

	const window = time.Minute
	for i := 0; i < 2; i++ {
		l.Allow("k", 2, window)
	}

	*advance = 30 * time.Second
	ok, retry := l.Allow("k", 2, window)
	if ok {
		t.Fatal("expected denial")
	}
	want := 30 * time.Second // 60s window minus 30s elapsed
	if retry != want {
		t.Fatalf("expected retry-after %v, got %v", want, retry)
	}
}

func TestAllowConcurrentIsSafe(t *testing.T) {
	l := NewLimiter()
	defer l.Stop()

	const goroutines = 8
	const perGoroutine = 10
	const limit = goroutines * perGoroutine

	var allowed atomic.Int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				if ok, _ := l.Allow("shared", limit, time.Hour); ok {
					allowed.Add(1)
				}
				l.Allow("ip:10.0.0.1", limit, time.Hour)
			}
		}()
	}
	wg.Wait()

	if got := allowed.Load(); got != limit {
		t.Fatalf("expected exactly %d allowed, got %d", limit, got)
	}
	if len(l.buckets) != 2 {
		t.Fatalf("expected 2 buckets, got %d", len(l.buckets))
	}
}

func TestJanitorPrunesExpiredBuckets(t *testing.T) {
	now, advance := fakeClock(time.Unix(1_700_000_000, 0))
	l := testLimiter(now)

	l.Allow("ip:expired", 10, time.Minute)
	l.Allow("ip:fresh", 10, time.Hour)

	*advance = 2 * time.Minute
	l.prune()

	if len(l.buckets) != 1 {
		t.Fatalf("expected 1 bucket after prune, got %d", len(l.buckets))
	}
	if _, ok := l.buckets["ip:fresh"]; !ok {
		t.Fatal("expected the fresh bucket to survive the prune")
	}
}

func TestJanitorStopHaltsGoroutine(t *testing.T) {
	l := NewLimiter()
	l.Stop()
	// Second stop must be a no-op (no panic).
	l.Stop()
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name   string
		xff    string
		remote string
		want   string
	}{
		{name: "remote addr only", remote: "203.0.113.5:5678", want: "203.0.113.5"},
		{name: "first xff wins", xff: "198.51.100.7, 203.0.113.9", remote: "10.0.0.1:1234", want: "198.51.100.7"},
		{name: "trims xff whitespace", xff: " 198.51.100.7 , 203.0.113.9", remote: "10.0.0.1:1234", want: "198.51.100.7"},
		{name: "xff present but empty falls back", xff: " ", remote: "203.0.113.5:1234", want: "203.0.113.5"},
		{name: "malformed remote addr passes through", remote: "not-an-addr", want: "not-an-addr"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			r.RemoteAddr = tt.remote
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			if got := ClientIP(r); got != tt.want {
				t.Fatalf("expected IP %q, got %q", tt.want, got)
			}
		})
	}
}

func TestAccountKeyNormalizesEmailAndRestoresBody(t *testing.T) {
	body := `{"email":"  Foo@Bar.COM  ","password":"x"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))

	key, ok := AccountKey(r)
	if !ok {
		t.Fatal("expected an account key for a well-formed body")
	}
	if key != "email:foo@bar.com" {
		t.Fatalf("expected normalized email key, got %q", key)
	}

	// The handler must still decode the full original body.
	decoded, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	var payload struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(decoded, &payload); err != nil {
		t.Fatalf("restored body did not decode: %v", err)
	}
	if payload.Email != "  Foo@Bar.COM  " || payload.Password != "x" {
		t.Fatalf("restored body altered: %+v", payload)
	}
}

func TestAccountKeySkipsWhenNoEmail(t *testing.T) {
	for _, body := range []string{`{"password":"x"}`, `not-json`, ``, `{"email":""}`} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
		if _, ok := AccountKey(r); ok {
			t.Fatalf("expected no account key for body %q", body)
		}
	}
}

func TestRateLimitMiddlewareReturns429(t *testing.T) {
	now, advance := fakeClock(time.Unix(1_700_000_000, 0))
	l := testLimiter(now)

	const limit = 2
	handler := RateLimit(l, Check{Rule: Rule{Limit: limit, Window: time.Minute}, Key: IPKey})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	do := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
		return rec
	}

	for i := 0; i < limit; i++ {
		if rec := do(); rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rec.Code)
		}
	}

	// Mid-window denial: 429 with a Retry-After reflecting the remaining window.
	*advance = 15 * time.Second
	rec := do()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "45" {
		t.Fatalf("expected Retry-After 45, got %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "too_many_requests") {
		t.Fatalf("expected generic code in body, got %s", body)
	}
	if strings.Contains(body, "x@y.z") {
		t.Fatal("429 body must not leak account identifiers")
	}
}

// TestRateLimitMiddlewareAccountKey proves the account bucket independently
// guards repeated logins with the same email even from distinct IPs.
func TestRateLimitMiddlewareAccountKey(t *testing.T) {
	now, _ := fakeClock(time.Unix(1_700_000_000, 0))
	l := testLimiter(now)

	handler := RateLimit(l,
		Check{Rule: Rule{Limit: 2, Window: time.Minute}, Key: IPKey},
		Check{Rule: Rule{Limit: 2, Window: time.Minute}, Key: AccountKey},
	)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	do := func(ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
			strings.NewReader(`{"email":"victim@example.com","password":"x"}`))
		req.Header.Set("X-Forwarded-For", ip)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	for i, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		want := http.StatusOK
		if i >= 2 {
			want = http.StatusTooManyRequests // 3rd login for the same account, from a fresh IP
		}
		if rec := do(ip); rec.Code != want {
			t.Fatalf("request %d: expected %d, got %d", i+1, want, rec.Code)
		}
	}
}
