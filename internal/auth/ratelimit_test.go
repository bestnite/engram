package auth

import (
	"context"
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
		Sleep:     func(context.Context, time.Duration) error { return nil },
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
