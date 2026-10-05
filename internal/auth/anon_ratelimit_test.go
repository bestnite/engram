package auth

import (
	"fmt"
	"testing"
	"time"
)

// clockAt 返回一个可推进的注入时钟，测试据此断言固定窗口的过期而无需真实等待。
type clockAt struct{ t time.Time }

func (c *clockAt) now() time.Time { return c.t }

// TestAnonymousLimiterFixedWindow 覆盖匿名入口限流器的固定窗口语义：
// 阈值内放行，超过即拒；窗口过期后重新放行。
func TestAnonymousLimiterFixedWindow(t *testing.T) {
	clock := &clockAt{t: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)}
	l := NewAnonymousLimiter(5, 15*time.Minute, clock.now)

	for i := 1; i <= 5; i++ {
		if !l.Allow("203.0.113.1", "a@example.com") {
			t.Fatalf("request %d within quota = denied, want allowed", i)
		}
	}
	if l.Allow("203.0.113.1", "a@example.com") {
		t.Fatal("6th request within the window = allowed, want denied")
	}

	// 窗口过期：同一 IP + 邮箱重新放行。
	clock.t = clock.t.Add(16 * time.Minute)
	if !l.Allow("203.0.113.1", "a@example.com") {
		t.Fatal("request after window expiry = denied, want allowed")
	}
}

// TestAnonymousLimiterIPDimensionAlone 断言 IP 维度独立成立：
// 同一 IP 换不同邮箱，第 6 次仍被拒（邮箱维度各自停在 1，不参与拦截）。
func TestAnonymousLimiterIPDimensionAlone(t *testing.T) {
	l := NewAnonymousLimiter(5, 15*time.Minute, nil)
	for i := 0; i < 5; i++ {
		if !l.Allow("203.0.113.1", fmt.Sprintf("ip-dim-%d@example.com", i)) {
			t.Fatalf("request %d = denied, want allowed", i+1)
		}
	}
	if l.Allow("203.0.113.1", "ip-dim-fresh@example.com") {
		t.Fatal("6th request with a fresh email from the same IP = allowed, want denied")
	}
}

// TestAnonymousLimiterEmailDimensionAlone 断言邮箱维度独立成立：
// 同一目标邮箱换不同 IP，第 6 次仍被拒（IP 维度各自停在 1，不参与拦截）。
func TestAnonymousLimiterEmailDimensionAlone(t *testing.T) {
	l := NewAnonymousLimiter(5, 15*time.Minute, nil)
	const email = "same@example.com"
	for i := 0; i < 5; i++ {
		if !l.Allow(fmt.Sprintf("203.0.113.%d", 10+i), email) {
			t.Fatalf("request %d = denied, want allowed", i+1)
		}
	}
	if l.Allow("203.0.113.99", email) {
		t.Fatal("6th request for the same email from a fresh IP = allowed, want denied")
	}
}
