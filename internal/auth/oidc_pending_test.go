package auth

import (
	"fmt"
	"testing"
	"time"
)

// F17：OIDC 的待完成登录表（OIDCClient.pending）是有界性与过期淘汰的验收测试。
//
// 该表是纯内存结构：匿名者可以反复 GET /auth/oidc/start，而只有带正确 state 的回调才会删除
// 对应项。没有上限与淘汰时它会被堆大（内存 DoS）。这里直接对 OIDCClient 做单元测试，
// 时钟由 c.now 注入，断言确定、不依赖真实时间流逝。

// pendingCap 是用户拍板的上限（1000 条）。测试用字面量钉住这个决定：实现若放宽/收紧，
// 有界性用例会失败。
const pendingCap = 1000

// fixedClock 构造一个时钟可注入的客户端，返回可推进的时间指针。
func fixedClock(t *testing.T) (*OIDCClient, *time.Time) {
	t.Helper()
	c := NewOIDCClient(nil)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	return c, &now
}

// TestOIDCPendingTableIsBounded：连续发起远超上限且都未过期的登录，表大小必须被上限压住。
func TestOIDCPendingTableIsBounded(t *testing.T) {
	c, now := fixedClock(t)
	exp := now.Add(10 * time.Minute)
	for i := 0; i < pendingCap*5; i++ {
		c.PutPending(fmt.Sprintf("state-%d", i), PendingAuth{ExpiresAt: exp})
	}
	if got := len(c.pending); got > pendingCap {
		t.Fatalf("pending table holds %d entries, want at most %d (unbounded anonymous growth)", got, pendingCap)
	}
}

// TestOIDCPendingSweepsExpiredEntries：过期项必须被清理，后续发起只留下自己那一条。
func TestOIDCPendingSweepsExpiredEntries(t *testing.T) {
	c, now := fixedClock(t)
	expired := now.Add(-time.Minute)
	for i := 0; i < pendingCap; i++ {
		c.PutPending(fmt.Sprintf("expired-%d", i), PendingAuth{ExpiresAt: expired})
	}
	// 再来一次发起：过期的都应被淘汰，表里只该留下这一条新记录。
	c.PutPending("fresh", PendingAuth{ExpiresAt: now.Add(10 * time.Minute)})
	if got := len(c.pending); got != 1 {
		t.Fatalf("after sweeping, pending table holds %d entries, want 1 (expired entries never cleaned)", got)
	}
	if _, ok := c.pending["fresh"]; !ok {
		t.Fatal("the fresh pending entry was dropped together with the expired ones")
	}
}

// TestOIDCPendingTakeIsOnceOnly：state 只能消费一次（重放被拒）。
func TestOIDCPendingTakeIsOnceOnly(t *testing.T) {
	c, now := fixedClock(t)
	c.PutPending("s1", PendingAuth{Nonce: "n", Verifier: "v", ExpiresAt: now.Add(10 * time.Minute)})
	if _, ok := c.TakePending("s1"); !ok {
		t.Fatal("first TakePending failed")
	}
	if _, ok := c.TakePending("s1"); ok {
		t.Fatal("second TakePending succeeded; a state was consumed twice")
	}
}

// TestOIDCPendingNormalFlowRoundTrips：正常流程（Put 后 Take）仍逐字段一致。
func TestOIDCPendingNormalFlowRoundTrips(t *testing.T) {
	c, now := fixedClock(t)
	want := PendingAuth{
		Nonce:       "nonce-1",
		Verifier:    "verifier-1",
		RedirectURI: "http://example.com/auth/oidc/callback",
		ExpiresAt:   now.Add(10 * time.Minute),
	}
	c.PutPending("s-normal", want)
	got, ok := c.TakePending("s-normal")
	if !ok {
		t.Fatal("TakePending after PutPending failed")
	}
	if got != want {
		t.Fatalf("TakePending = %+v, want %+v", got, want)
	}
}
