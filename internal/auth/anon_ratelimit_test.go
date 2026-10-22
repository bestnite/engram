package auth

import (
	"fmt"
	"testing"
	"time"
)

// clockAt returns an injectable clock that can be advanced, so tests can assert fixed-window expiry without real waiting.
type clockAt struct{ t time.Time }

func (c *clockAt) now() time.Time { return c.t }

// TestAnonymousLimiterFixedWindow covers the fixed-window semantics of the anonymous-entry limiter:
// requests within the threshold pass and beyond it are rejected; once the window expires they pass again.
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

	// Window expired: the same IP + email pass again.
	clock.t = clock.t.Add(16 * time.Minute)
	if !l.Allow("203.0.113.1", "a@example.com") {
		t.Fatal("request after window expiry = denied, want allowed")
	}
}

// TestAnonymousLimiterIPDimensionAlone asserts that the IP dimension holds on its own:
// for the same IP with a different email each time, the 6th attempt is still rejected (the email dimension stays at 1 each and takes no part in blocking).
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

// TestAnonymousLimiterEmailDimensionAlone asserts that the email dimension holds on its own:
// for the same target email from a different IP each time, the 6th attempt is still rejected (the IP dimension stays at 1 each and takes no part in blocking).
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
