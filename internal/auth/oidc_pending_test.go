package auth

import (
	"fmt"
	"testing"
	"time"
)

// Acceptance tests for boundedness and expiry eviction of the OIDC pending-login table (OIDCClient.pending).
//
// The table is a pure in-memory structure: anonymous visitors can repeatedly GET /auth/oidc/start, and only a callback with the correct state deletes
// the corresponding entry. Without a cap and eviction it grows without bound (memory DoS). Here OIDCClient is unit-tested directly,
// with the clock injected via c.now, so assertions are deterministic and do not depend on real time passing.

// pendingCap is the cap the user settled on (1000 entries). The test pins that decision with a literal: if the implementation loosens or tightens it,
// the boundedness case fails.
const pendingCap = 1000

// fixedClock constructs a client with an injectable clock and returns an advanceable time pointer.
func fixedClock(t *testing.T) (*OIDCClient, *time.Time) {
	t.Helper()
	c := NewOIDCClient(nil)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	return c, &now
}

// TestOIDCPendingTableIsBounded: starting far more logins than the cap, all unexpired, the table size must stay capped.
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

// TestOIDCPendingSweepsExpiredEntries: expired entries must be cleaned up, so a later start leaves only its own record.
func TestOIDCPendingSweepsExpiredEntries(t *testing.T) {
	c, now := fixedClock(t)
	expired := now.Add(-time.Minute)
	for i := 0; i < pendingCap; i++ {
		c.PutPending(fmt.Sprintf("expired-%d", i), PendingAuth{ExpiresAt: expired})
	}
	// One more start: the expired ones should be evicted, leaving only this new record in the table.
	c.PutPending("fresh", PendingAuth{ExpiresAt: now.Add(10 * time.Minute)})
	if got := len(c.pending); got != 1 {
		t.Fatalf("after sweeping, pending table holds %d entries, want 1 (expired entries never cleaned)", got)
	}
	if _, ok := c.pending["fresh"]; !ok {
		t.Fatal("the fresh pending entry was dropped together with the expired ones")
	}
}

// TestOIDCPendingTakeIsOnceOnly: a state can be consumed only once (replay is rejected).
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

// TestOIDCPendingNormalFlowRoundTrips: the normal flow (Put then Take) still matches field by field.
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
