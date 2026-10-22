package mcp

import (
	"fmt"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// sessionRemembered reports whether the session is registered in the owner table (reading the internal map directly, for case assertions).
func sessionRemembered(s *Server, sid string) bool {
	_, ok := s.sessions[sid]
	return ok
}

// TestSessionForgottenAfterDelete asserts that after the client closes the session normally with DELETE /mcp, the entry is gone from the owner table.
// The old implementation only cleaned up on a 404 response, so the entry of a session deleted successfully (2xx) lingered forever, growing without bound in a long-running process (F10c).
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

// TestSessionTableEvictsOldestOverCap asserts the owner table evicts the oldest entry by registration time once it exceeds the capacity cap,
// while the newest entry and the session just registered are both kept (normal sessions are unaffected).
func TestSessionTableEvictsOldestOverCap(t *testing.T) {
	_, _, _, srv, _ := newEnvWithServer(t)

	// Inject a monotonically increasing clock so each record's seenAt is strictly ordered and the eviction outcome is deterministic.
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

	// Registering entry cap+1: the oldest, s-0, should be evicted.
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

// TestForgetSessionLeavesOtherSessionsIntact asserts clearing one session's owner record does not affect other sessions.
func TestForgetSessionLeavesOtherSessionsIntact(t *testing.T) {
	_, _, _, srv, _ := newEnvWithServer(t)
	srv.rememberSession("a", 1)
	srv.rememberSession("b", 2)
	srv.forgetSession("a")
	if !sessionRemembered(srv, "b") {
		t.Fatal("forgetSession(\"a\") removed the unrelated session \"b\"")
	}
}
