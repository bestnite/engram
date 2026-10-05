package mcp

import (
	"fmt"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// sessionRemembered 报告属主表里是否登记了该会话（直接读内部 map，供用例断言）。
func sessionRemembered(s *Server, sid string) bool {
	_, ok := s.sessions[sid]
	return ok
}

// TestSessionForgottenAfterDelete 断言客户端用 DELETE /mcp 正常关闭会话后，属主表里不再有该条目。
// 旧实现只在响应 404 时清理，DELETE 成功（2xx）的会话条目会一直残留，长期运行无界增长（F10c）。
func TestSessionForgottenAfterDelete(t *testing.T) {
	_, db, keys, srv, ts := newEnvWithServer(t)
	u := seedUser(t, db, "session-owner")
	key := newKey(t, keys, u.ID, []string{store.ScopeRead})
	cs := connect(t, ts.URL, key)

	sid := cs.ID()
	if sid == "" {
		t.Fatal("client session has no id")
	}
	if !sessionRemembered(srv, sid) {
		t.Fatalf("session %q not remembered after initialize", sid)
	}
	if err := cs.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}
	if sessionRemembered(srv, sid) {
		t.Fatalf("session %q still remembered after DELETE /mcp", sid)
	}
}

// TestSessionTableEvictsOldestOverCap 断言属主表超过容量上限后按登记时刻淘汰最旧条目，
// 且最新的条目与刚登记的会话都保留（正常会话不受影响）。
func TestSessionTableEvictsOldestOverCap(t *testing.T) {
	_, _, _, srv, _ := newEnvWithServer(t)

	// 注入单调递增时钟，使每条记录的 seenAt 严格有序，淘汰结果确定。
	tick := int64(0)
	srv.clock = func() time.Time {
		tick++
		return time.Unix(0, 0).Add(time.Duration(tick) * time.Second)
	}

	for i := 0; i < mcpSessionCap; i++ {
		srv.rememberSession(fmt.Sprintf("s-%d", i), 1)
	}
	if got := len(srv.sessions); got != mcpSessionCap {
		t.Fatalf("table size = %d, want %d", got, mcpSessionCap)
	}

	// 第 cap+1 条登记：最旧的 s-0 应被淘汰。
	srv.rememberSession("newest", 1)
	if sessionRemembered(srv, "s-0") {
		t.Errorf("oldest entry s-0 was not evicted after exceeding the cap")
	}
	if !sessionRemembered(srv, fmt.Sprintf("s-%d", mcpSessionCap-1)) {
		t.Errorf("entry s-%d was evicted but is not the oldest", mcpSessionCap-1)
	}
	if !sessionRemembered(srv, "newest") {
		t.Errorf("the just-registered session was not kept")
	}
	if got := len(srv.sessions); got != mcpSessionCap {
		t.Errorf("table size = %d after eviction, want %d (cap)", got, mcpSessionCap)
	}
}

// TestForgetSessionLeavesOtherSessionsIntact 断言清掉一个会话的属主记录不影响其它会话。
func TestForgetSessionLeavesOtherSessionsIntact(t *testing.T) {
	_, _, _, srv, _ := newEnvWithServer(t)
	srv.rememberSession("a", 1)
	srv.rememberSession("b", 2)
	srv.forgetSession("a")
	if !sessionRemembered(srv, "b") {
		t.Fatal("forgetSession(\"a\") removed the unrelated session \"b\"")
	}
}
