package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// Default parameters for login rate limiting. The delay grows as base × 2^(failure count-1) and is capped at MaxDelay.
const (
	DefaultRateLimitBaseDelay = 200 * time.Millisecond
	DefaultRateLimitMaxDelay  = 5 * time.Second
	DefaultRateLimitWindow    = 15 * time.Minute
	// DefaultMaxAccountFailures is the number of failures an account may have within the window; once reached, attempts are rejected outright until the window ends.
	// The password and the TOTP second step share this count: a 6-digit TOTP cannot be brute-forced at 10 tries per 15 minutes.
	DefaultMaxAccountFailures = 10
	// DefaultMaxIPFailures is the number of failures an IP may have within the window; it is looser than the account limit because several legitimate users
	// may sit behind the same egress IP.
	DefaultMaxIPFailures = 50
	// inflightTTL is the maximum lifetime of an "admitted, result not reported yet" attempt placeholder. The normal path returns the placeholder in
	// RecordFailure / Reset / Release; a request that errors out midway does not return it, and the placeholder expires on its own.
	inflightTTL = time.Minute
)

// ErrLoginLocked means the account or the IP has reached its failure limit, so this attempt is rejected outright (no authentication is performed).
var ErrLoginLocked = errors.New("login temporarily locked")

// LockedError carries how long remains before the lock lifts, for the Retry-After response header.
type LockedError struct{ RetryAfter time.Duration }

func (e *LockedError) Error() string { return ErrLoginLocked.Error() }

// Is makes errors.Is(err, ErrLoginLocked) hold.
func (e *LockedError) Is(target error) bool { return target == ErrLoginLocked }

// LimiterConfig is the limiter configuration; zero-valued fields are defaulted by NewLoginLimiter.
type LimiterConfig struct {
	BaseDelay time.Duration
	MaxDelay  time.Duration
	// Window is the freshness period of the failure count: with no new failure within it, the count is treated as cleared.
	Window time.Duration
	// MaxAccountFailures / MaxIPFailures are the lock thresholds of the two dimensions; <=0 means the default.
	MaxAccountFailures int
	MaxIPFailures      int
	// Now is an injectable clock; tests use it to expire a window without real waiting.
	Now func() time.Time
	// Sleep is an injectable wait function; tests inject a recorder so real sleeping does not slow the suite down.
	Sleep func(ctx context.Context, d time.Duration) error
}

// failureBucket holds the failure count and the in-flight attempt count for a single key (account or IP).
type failureBucket struct {
	count  int
	lastAt time.Time
	// inflight is the number of attempts that already passed Wait and have not reported a result yet. The lock decision counts it in: looking only at recorded failures,
	// a burst of concurrent requests would all clear the check before the first failure is recorded, and the threshold would be meaningless.
	inflight   int
	inflightAt time.Time
}

// LoginLimiter records login failures along two dimensions, account and IP: the more failures, the longer the delay for later attempts;
// once the failure count (in-flight attempts included) reaches the threshold, attempts are rejected outright until the window passes.
//
// With delay alone and no locking, an attacker could amplify throughput arbitrarily with concurrency (every request just sleeps a few extra seconds).
// The price of locking is that someone can deliberately mistype to lock an account for one window (15 minutes); for a self-hosted instance that is
// an acceptable trade-off, and a successful login clears it immediately.
//
// State lives in memory: this service is deployed as a single instance and pulls in no Redis; failure counts are short-lived anti-brute-force data,
// so clearing them on process restart is an acceptable trade-off.
type LoginLimiter struct {
	mu         sync.Mutex
	buckets    map[string]*failureBucket
	base       time.Duration
	max        time.Duration
	window     time.Duration
	maxAccount int
	maxIP      int
	now        func() time.Time
	sleep      func(ctx context.Context, d time.Duration) error
}

// NewLoginLimiter constructs the limiter and fills in defaults; every dependency is injectable, which keeps it testable.
func NewLoginLimiter(cfg LimiterConfig) *LoginLimiter {
	l := &LoginLimiter{
		buckets:    make(map[string]*failureBucket),
		base:       cfg.BaseDelay,
		max:        cfg.MaxDelay,
		window:     cfg.Window,
		maxAccount: cfg.MaxAccountFailures,
		maxIP:      cfg.MaxIPFailures,
		now:        cfg.Now,
		sleep:      cfg.Sleep,
	}
	if l.base <= 0 {
		l.base = DefaultRateLimitBaseDelay
	}
	if l.max <= 0 {
		l.max = DefaultRateLimitMaxDelay
	}
	if l.max < l.base {
		l.max = l.base
	}
	if l.window <= 0 {
		l.window = DefaultRateLimitWindow
	}
	if l.maxAccount <= 0 {
		l.maxAccount = DefaultMaxAccountFailures
	}
	if l.maxIP <= 0 {
		l.maxIP = DefaultMaxIPFailures
	}
	if l.now == nil {
		l.now = func() time.Time { return time.Now().UTC() }
	}
	if l.sleep == nil {
		l.sleep = sleepContext
	}
	return l
}

// sleepContext is a wait that ctx can cancel; when the request disconnects it should return at once instead of holding the connection.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func accountKey(username string) string {
	return "user:" + strings.ToLower(strings.TrimSpace(username))
}
func ipKey(ip string) string { return "ip:" + strings.TrimSpace(ip) }

// keys returns the two keys involved in this attempt and their thresholds; an empty account name or empty IP gets no bucket (their keys have no
// discrimination, so a bucket would only pollute the others).
func (l *LoginLimiter) keys(username, ip string) []struct {
	key   string
	limit int
} {
	out := make([]struct {
		key   string
		limit int
	}, 0, 2)
	if k := accountKey(username); k != "user:" {
		out = append(out, struct {
			key   string
			limit int
		}{k, l.maxAccount})
	}
	if k := ipKey(ip); k != "ip:" {
		out = append(out, struct {
			key   string
			limit int
		}{k, l.maxIP})
	}
	return out
}

// bucket fetches a key's bucket and prunes the expired part by time; returns nil when absent (the caller holds the lock).
func (l *LoginLimiter) bucket(key string, now time.Time) *failureBucket {
	b, ok := l.buckets[key]
	if !ok {
		return nil
	}
	if b.count > 0 && (b.lastAt.IsZero() || now.Sub(b.lastAt) > l.window) {
		b.count = 0
	}
	if b.inflight > 0 && now.Sub(b.inflightAt) > inflightTTL {
		b.inflight = 0
	}
	if b.count == 0 && b.inflight == 0 {
		delete(l.buckets, key)
		return nil
	}
	return b
}

// delayFor converts a failure count into a wait duration: 0 failures wait nothing, after that it grows by powers of two up to the cap.
func (l *LoginLimiter) delayFor(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	d := l.base
	for i := 1; i < failures; i++ {
		d *= 2
		if d >= l.max {
			return l.max
		}
	}
	if d > l.max {
		d = l.max
	}
	return d
}

// Wait admits or rejects one attempt before authentication.
//
// When either dimension's "recorded failures + in-flight attempts" reaches its threshold, it returns *LockedError immediately (errors.Is matches
// ErrLoginLocked) without waiting or taking a placeholder; otherwise it takes one in-flight slot in each dimension and then waits an increasing
// amount based on the accumulated failure count, returning this attempt's delay (for logs and test assertions). The larger of the two dimensions wins: either one
// hitting the limit should slow things down, otherwise switching accounts against the same IP would bypass it.
//
// Once the authentication result is in, the caller must call exactly one of RecordFailure (failure), Reset (success) or Release (not counted either way,
// e.g. the first factor passed and the second is pending) to return the placeholder; a forgotten placeholder expires after inflightTTL.
func (l *LoginLimiter) Wait(ctx context.Context, username, ip string) (time.Duration, error) {
	l.mu.Lock()
	now := l.now()
	failures := 0
	var retry time.Duration
	locked := false
	keys := l.keys(username, ip)
	for _, k := range keys {
		b := l.bucket(k.key, now)
		if b == nil {
			continue
		}
		if b.count > failures {
			failures = b.count
		}
		if b.count+b.inflight >= k.limit {
			locked = true
			if r := l.window - now.Sub(b.lastAt); b.count > 0 && r > retry {
				retry = r
			}
			if r := inflightTTL - now.Sub(b.inflightAt); b.inflight > 0 && r > retry {
				retry = r
			}
		}
	}
	if locked {
		l.mu.Unlock()
		if retry <= 0 {
			retry = time.Second
		}
		return 0, &LockedError{RetryAfter: retry}
	}
	for _, k := range keys {
		b := l.buckets[k.key]
		if b == nil {
			b = &failureBucket{}
			l.buckets[k.key] = b
		}
		b.inflight++
		b.inflightAt = now
	}
	l.mu.Unlock()
	delay := l.delayFor(failures)
	if delay <= 0 {
		return 0, nil
	}
	if err := l.sleep(ctx, delay); err != nil {
		l.Release(username, ip)
		return delay, err
	}
	return delay, nil
}

// RecordFailure records one failure, returns Wait's placeholder, and reports the account dimension's new failure count;
// both the account and the IP dimension are incremented.
func (l *LoginLimiter) RecordFailure(username, ip string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	count := 0
	account := accountKey(username)
	for _, k := range l.keys(username, ip) {
		b := l.bucket(k.key, now)
		if b == nil {
			b = &failureBucket{}
			l.buckets[k.key] = b
		}
		if b.inflight > 0 {
			b.inflight--
		}
		b.count++
		b.lastAt = now
		if k.key == account {
			count = b.count
		}
	}
	return count
}

// Release returns Wait's placeholder without recording success or failure: used when the first factor passed and the second is still pending.
func (l *LoginLimiter) Release(username, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for _, k := range l.keys(username, ip) {
		if b := l.bucket(k.key, now); b != nil && b.inflight > 0 {
			b.inflight--
			if b.count == 0 && b.inflight == 0 {
				delete(l.buckets, k.key)
			}
		}
	}
}

// Reset clears the failure counts and placeholders of the account and the IP after a successful login (acceptance: reset on success).
func (l *LoginLimiter) Reset(username, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, accountKey(username))
	delete(l.buckets, ipKey(ip))
}

// Default parameters for anonymous-entry rate limiting: pre-login entry points such as /forgot-password and /register
// can be bombarded anonymously to trigger mail, so they get fixed-window limiting along the IP and target-email dimensions. The thresholds were decided by nite:
// 5 requests / 15 minutes per dimension.
const (
	DefaultAnonRateLimit  = 5
	DefaultAnonRateWindow = 15 * time.Minute
)

// anonBucket is the request count for one key (IP or email) within a fixed window.
type anonBucket struct {
	count int
	start time.Time
}

// AnonymousLimiter counts anonymous-entry requests along the two dimensions "requester IP" and "target email" in fixed windows,
// rejecting as soon as either dimension exceeds its limit within a window.
//
// It has the same shape as LoginLimiter: state in memory (single-instance deployment), injectable clock so
// tests can assert window expiry without real waiting. The difference is that it ignores whether the result was right and only caps the request count — because
// registration and password recovery are entry points where legitimate requests can also be frequent, so abuse cannot be measured as "one failure, one count".
type AnonymousLimiter struct {
	mu      sync.Mutex
	buckets map[string]*anonBucket
	limit   int
	window  time.Duration
	now     func() time.Time
}

// NewAnonymousLimiter constructs the limiter and fills in defaults; now is injectable and falls back to the UTC wall clock when nil.
func NewAnonymousLimiter(limit int, window time.Duration, now func() time.Time) *AnonymousLimiter {
	l := &AnonymousLimiter{
		buckets: make(map[string]*anonBucket),
		limit:   limit,
		window:  window,
		now:     now,
	}
	if l.limit <= 0 {
		l.limit = DefaultAnonRateLimit
	}
	if l.window <= 0 {
		l.window = DefaultAnonRateWindow
	}
	if l.now == nil {
		l.now = func() time.Time { return time.Now().UTC() }
	}
	return l
}

// allowLocked records one request for a dimension and reports whether it is still within quota (the caller holds the lock).
// Empty values get no bucket: keys without discrimination would only pollute each other (consistent with RecordFailure's handling of empty keys).
func (l *AnonymousLimiter) allowLocked(prefix, value string, now time.Time) bool {
	if value == "" {
		return true
	}
	key := prefix + value
	b, ok := l.buckets[key]
	if !ok || now.Sub(b.start) >= l.window {
		l.buckets[key] = &anonBucket{count: 1, start: now}
		return true
	}
	b.count++
	return b.count <= l.limit
}

// Allow records one anonymous-entry request and reports whether it passes: either the IP or the target email over the limit rejects it.
//
// Both dimensions are counted (consistent with the fixed-window limiter convention): once a dimension goes over the limit it keeps being
// rejected until the window expires. The email is lowercased and trimmed first, so case differences cannot bypass the email dimension's count.
func (l *AnonymousLimiter) Allow(ip, email string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	okIP := l.allowLocked("ip:", strings.TrimSpace(ip), now)
	okEmail := l.allowLocked("email:", strings.ToLower(strings.TrimSpace(email)), now)
	return okIP && okEmail
}
