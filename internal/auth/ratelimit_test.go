package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// sleepRecorder 记录限流器请求的等待时长而不真的睡；测试据此断言延迟递增。
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

// TestLoginLimiterDelayGrowsAndResetsOnSuccess 是验收测试：
// 失败越多延迟越长，登录成功后归零。
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

	// 没有任何失败时不等。
	if d, err := lim.Wait(ctx, "alice", "127.0.0.1"); err != nil || d != 0 {
		t.Fatalf("Wait() before any failure = (%v, %v), want (0, nil)", d, err)
	}

	// 连续失败：每次等待必须严格大于上一次。
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
	// 期望的确定性递增：base, 2*base, 4*base, 8*base。
	if prev != 8*base {
		t.Errorf("4th delay = %v, want %v", prev, 8*base)
	}

	// 登录成功后重置：延迟归零（验收点）。
	lim.Reset("alice", "127.0.0.1")
	if d, err := lim.Wait(ctx, "alice", "127.0.0.1"); err != nil || d != 0 {
		t.Fatalf("Wait() after Reset = (%v, %v), want (0, nil)", d, err)
	}
}

// TestLoginLimiterCapsDelay 断言延迟封顶，避免失败次数把等待时间推到不可接受的长度。
func TestLoginLimiterCapsDelay(t *testing.T) {
	lim := NewLoginLimiter(LimiterConfig{
		BaseDelay: 10 * time.Millisecond,
		MaxDelay:  40 * time.Millisecond,
		Window:    time.Hour,
		// 阈值放宽到 20 次失败之上：本用例只验延迟封顶，不验锁定。
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

// TestLoginLimiterRateLimitsIPIndependently 断言换账号刷同一 IP 也会被限流。
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

// TestLoginLimiterWindowExpiresFailures 用可注入时钟断言窗口过后失败计数清零（无需真实等待）。
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

// TestLoginLimiterLocksAfterThreshold 覆盖锁定：账号或 IP 的失败次数达到阈值后，Wait 直接返回
// ErrLoginLocked 与剩余等待时长；窗口过去后恢复；成功登录立即清零。
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

	// IP 维度：同一 IP 换账号累计到 5 次，新账号也被拒。
	fail("carol", "10.0.0.2")
	fail("dave", "10.0.0.2")
	fail("erin", "10.0.0.2")
	fail("frank", "10.0.0.2")
	fail("grace", "10.0.0.2")
	if !locked("heidi", "10.0.0.2") {
		t.Error("IP at threshold: a fresh account was allowed")
	}

	// 窗口过去后恢复。
	now = now.Add(16 * time.Minute)
	if locked("alice", "10.0.0.1") {
		t.Error("account still locked after the window expired")
	}
}

// TestLoginLimiterCountsConcurrentAttempts 是反面用例：同时发出的一批尝试在任何失败被记录之前
// 都会到达 Wait；在途尝试必须算进阈值，否则并发就能绕过锁定。
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
	// 归还占位（第一因素通过、等待第二因素的路径）后又能放行。
	lim.Release("alice", "10.1.0.0")
	if _, err := lim.Wait(ctx, "alice", "10.1.0.0"); err != nil {
		t.Errorf("Wait() after Release error = %v, want admitted", err)
	}
}

// TestLoginLimiterInflightExpires 断言没有归还的占位（请求中途出错返回）会自行失效，
// 不会把账号永久锁住。
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
