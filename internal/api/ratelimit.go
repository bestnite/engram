package api

import (
	"sync"
	"time"
)

// Default parameters for per-key rate limiting (60 req/min by default, stricter for writes).
const (
	DefaultReadLimit  = 60
	DefaultWriteLimit = 30
	DefaultRateWindow = time.Minute
)

// rateBucket is the request count for one key within a fixed window.
type rateBucket struct {
	count int
	start time.Time
}

// RateLimiter is an in-memory limiter counting per key or per user.
//
// State lives in memory: this service is deployed as a single instance and does not pull in Redis; rate-limit state is short-lived protection data,
// so clearing it on process restart is an acceptable trade-off. The clock is injectable, so tests need no real waiting.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rateBucket
	limit   int
	window  time.Duration
	now     func() time.Time
}

// NewRateLimiter builds a limiter; limit <= 0 uses DefaultReadLimit, window <= 0 uses DefaultRateWindow.
func NewRateLimiter(limit int, window time.Duration, now func() time.Time) *RateLimiter {
	l := &RateLimiter{
		buckets: make(map[string]*rateBucket),
		limit:   limit,
		window:  window,
		now:     now,
	}
	if l.limit <= 0 {
		l.limit = DefaultReadLimit
	}
	if l.window <= 0 {
		l.window = DefaultRateWindow
	}
	if l.now == nil {
		l.now = func() time.Time { return time.Now().UTC() }
	}
	return l
}

// Allow records one request and reports whether it is still within quota (fixed window: an expired window restarts the count).
func (l *RateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok || now.Sub(b.start) >= l.window {
		l.buckets[key] = &rateBucket{count: 1, start: now}
		return true
	}
	b.count++
	return b.count <= l.limit
}
