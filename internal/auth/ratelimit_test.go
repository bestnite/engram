package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// sleepRecorder records the wait duration the limiter asks for without actually sleeping; tests use it to assert the growing delay.
type sleepRecorder struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (r *sleepRecorder) sleep(_ context.Context, d time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.delays = append(r.delays, d)
	return nil
}

func (r *sleepRecorder) last() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.delays) == 0 {
		return 0
	}
	return r.delays[len(r.delays)-1]
}

// TestLoginLimiterDelayGrowsAndResetsOnSuccess is the acceptance test:
// the more failures, the longer the delay, and after a successful login it drops to zero.
func TestLoginLimiterDelayGrowsAndResetsOnSuccess(t *testing.T) {
	rec := &sleepRecorder{}
	base := 10 * time.Millisecond
	lim := NewLoginLimiter(LimiterConfig{
		BaseDelay: base,
		MaxDelay:  1 * time.Second,
		Window:    time.Hour,
		Sleep:     rec.sleep,
	})
	ctx := context.Background()

	// With no failures at all there is no wait.
	if d, err := lim.Wait(ctx, "alice", "127.0.0.1"); err != nil || d != 0 {
		t.Fatalf("Wait() before any failure = (%v, %v), want (0, nil)", d, err)
	}

	// Consecutive failures: each wait must be strictly greater than the previous one.
	var prev time.Duration
	for i := 1; i <= 4; i++ {
		lim.RecordFailure("alice", "127.0.0.1")
		d, err := lim.Wait(ctx, "alice", "127.0.0.1")
		if err != nil {
			t.Fatalf("Wait() after failure %d error = %v", i, err)
		}
		if d <= prev {
			t.Fatalf("delay after failure %d = %v, want > %v (must grow)", i, d, prev)
		}
		prev = d
		if rec.last() != d {
			t.Errorf("recorded sleep = %v, Wait returned %v", rec.last(), d)
		}
	}
	// The deterministic growth expected: base, 2*base, 4*base, 8*base.
	if prev != 8*base {
		t.Errorf("4th delay = %v, want %v", prev, 8*base)
	}

	// Reset after a successful login: the delay is back to zero (acceptance point).
	lim.Reset("alice", "127.0.0.1")
	if d, err := lim.Wait(ctx, "alice", "127.0.0.1"); err != nil || d != 0 {
		t.Fatalf("Wait() after Reset = (%v, %v), want (0, nil)", d, err)
	}
}

// TestLoginLimiterCapsDelay asserts the delay is capped, so the failure count cannot push the wait to an unacceptable length.
func TestLoginLimiterCapsDelay(t *testing.T) {
	lim := NewLoginLimiter(LimiterConfig{
		BaseDelay: 10 * time.Millisecond,
		MaxDelay:  40 * time.Millisecond,
		Window:    time.Hour,
		// Threshold relaxed above 20 failures: this case only verifies the delay cap, not locking.
		MaxAccountFailures: 100,
		Sleep:              func(context.Context, time.Duration) error { return nil },
	})
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		lim.RecordFailure("alice", "127.0.0.1")
	}
	if d, err := lim.Wait(ctx, "alice", "127.0.0.1"); err != nil || d != 40*time.Millisecond {
		t.Fatalf("Wait() after many failures = (%v, %v), want (%v, nil)", d, err, 40*time.Millisecond)
	}
}

// TestLoginLimiterRateLimitsIPIndependently asserts that switching accounts against the same IP is rate-limited too.
func TestLoginLimiterRateLimitsIPIndependently(t *testing.T) {
	lim := NewLoginLimiter(LimiterConfig{
		BaseDelay: 10 * time.Millisecond,
		Window:    time.Hour,
		Sleep:     func(context.Context, time.Duration) error { return nil },
	})
	ctx := context.Background()
	lim.RecordFailure("alice", "127.0.0.2")
	if d, err := lim.Wait(ctx, "bob", "127.0.0.2"); err != nil || d <= 0 {
		t.Fatalf("Wait() for a different account on the same IP = (%v, %v), want > 0", d, err)
	}
}

// TestLoginLimiterWindowExpiresFailures uses an injectable clock to assert the failure count clears after the window (no real waiting needed).
func TestLoginLimiterWindowExpiresFailures(t *testing.T) {
	now := time.Now().UTC()
	lim := NewLoginLimiter(LimiterConfig{
		BaseDelay: 10 * time.Millisecond,
		Window:    time.Minute,
		Now:       func() time.Time { return now },
		Sleep:     func(context.Context, time.Duration) error { return nil },
	})
	ctx := context.Background()
	lim.RecordFailure("alice", "127.0.0.3")
	if d, _ := lim.Wait(ctx, "alice", "127.0.0.3"); d <= 0 {
		t.Fatalf("Wait() right after a failure = %v, want > 0", d)
	}
	now = now.Add(2 * time.Minute)
	if d, err := lim.Wait(ctx, "alice", "127.0.0.3"); err != nil || d != 0 {
		t.Fatalf("Wait() after the window expired = (%v, %v), want (0, nil)", d, err)
	}
}

// TestLoginLimiterLocksAfterThreshold covers locking: once an account's or an IP's failure count reaches the threshold, Wait returns
// ErrLoginLocked and the remaining wait straight away; it recovers once the window passes; a successful login clears it immediately.
func TestLoginLimiterLocksAfterThreshold(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	lim := NewLoginLimiter(LimiterConfig{
		BaseDelay: time.Millisecond, Window: 15 * time.Minute,
		MaxAccountFailures: 3, MaxIPFailures: 5,
		Now:   func() time.Time { return now },
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	ctx := context.Background()
	fail := func(user, ip string) {
		t.Helper()
		if _, err := lim.Wait(ctx, user, ip); err != nil {
			t.Fatalf("Wait(%s, %s) before the threshold error = %v", user, ip, err)
		}
		lim.RecordFailure(user, ip)
	}
	locked := func(user, ip string) bool {
		t.Helper()
		_, err := lim.Wait(ctx, user, ip)
		if err == nil {
			lim.Release(user, ip)
			return false
		}
		if !errors.Is(err, ErrLoginLocked) {
			t.Fatalf("Wait(%s, %s) error = %v, want ErrLoginLocked", user, ip, err)
		}
		var le *LockedError
		if !errors.As(err, &le) || le.RetryAfter <= 0 || le.RetryAfter > 15*time.Minute {
			t.Fatalf("locked error = %#v, want a RetryAfter within the window", err)
		}
		return true
	}

	for i := 0; i < 3; i++ {
		fail("alice", "10.0.0.1")
	}
	cases := []struct {
		name       string
		user, ip   string
		wantLocked bool
	}{
		{"account at threshold is locked", "alice", "10.0.0.1", true},
		{"account is locked from another IP too", "alice", "10.0.0.9", true},
		{"other account on the same IP is still allowed", "bob", "10.0.0.1", false},
	}
	for _, tc := range cases {
		if got := locked(tc.user, tc.ip); got != tc.wantLocked {
			t.Errorf("%s: locked = %v, want %v", tc.name, got, tc.wantLocked)
		}
	}

	// IP dimension: switching accounts on the same IP accumulates to 5, and the new account is rejected as well.
	fail("carol", "10.0.0.2")
	fail("dave", "10.0.0.2")
	fail("erin", "10.0.0.2")
	fail("frank", "10.0.0.2")
	fail("grace", "10.0.0.2")
	if !locked("heidi", "10.0.0.2") {
		t.Error("IP at threshold: a fresh account was allowed")
	}

	// Recovers once the window passes.
	now = now.Add(16 * time.Minute)
	if locked("alice", "10.0.0.1") {
		t.Error("account still locked after the window expired")
	}
}

// TestLoginLimiterCountsConcurrentAttempts is the negative case: a burst of simultaneous attempts all reach Wait before any failure is recorded;
// in-flight attempts must count toward the threshold, otherwise concurrency alone gets around the lock.
func TestLoginLimiterCountsConcurrentAttempts(t *testing.T) {
	lim := NewLoginLimiter(LimiterConfig{
		BaseDelay: time.Millisecond, Window: time.Hour, MaxAccountFailures: 4, MaxIPFailures: 1000,
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	ctx := context.Background()
	const attempts = 40
	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := 0
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := lim.Wait(ctx, "alice", fmt.Sprintf("10.1.0.%d", i)); err == nil {
				mu.Lock()
				admitted++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if admitted != 4 {
		t.Fatalf("admitted %d concurrent attempts, want exactly the threshold 4", admitted)
	}
	// After the placeholder is returned (the path where the first factor passed and the second is pending) it passes again.
	lim.Release("alice", "10.1.0.0")
	if _, err := lim.Wait(ctx, "alice", "10.1.0.0"); err != nil {
		t.Errorf("Wait() after Release error = %v, want admitted", err)
	}
}

// TestLoginLimiterInflightExpires asserts that a placeholder nobody returned (the request errored out midway) expires by itself
// and does not lock the account forever.
func TestLoginLimiterInflightExpires(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	lim := NewLoginLimiter(LimiterConfig{
		BaseDelay: time.Millisecond, Window: time.Hour, MaxAccountFailures: 2,
		Now:   func() time.Time { return now },
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := lim.Wait(ctx, "alice", "10.2.0.1"); err != nil {
			t.Fatalf("Wait() %d error = %v", i, err)
		}
	}
	if _, err := lim.Wait(ctx, "alice", "10.2.0.1"); !errors.Is(err, ErrLoginLocked) {
		t.Fatalf("Wait() with two unreleased attempts error = %v, want ErrLoginLocked", err)
	}
	now = now.Add(inflightTTL + time.Second)
	if _, err := lim.Wait(ctx, "alice", "10.2.0.1"); err != nil {
		t.Errorf("Wait() after the in-flight slots expired error = %v, want admitted", err)
	}
}
