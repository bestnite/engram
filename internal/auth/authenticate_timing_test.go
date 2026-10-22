package auth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

// countVerifications wires the onVerify hook to a counter, as an observable proxy for "did argon2id actually run".
//
// Why not compare wall-clock milliseconds: that kind of assertion invariably flaps on shared CI and only produces unreliable red;
// equal cost is guaranteed by construction (the dummy hash is generated with the same hasher parameters), so the test only verifies that both paths ran the check.
func countVerifications(h *PasswordHasher) *atomic.Int64 {
	var n atomic.Int64
	h.onVerify = func() { n.Add(1) }
	return &n
}

// TestAuthenticate_NoUsernameEnumerationViaSkippedVerification asserts that login verification runs argon2id once for
// both "existing user + wrong password" and "nonexistent user".
// As things stand, the nonexistent-user branch returns immediately and spends no hash computation, so usernames can be enumerated by response time.
func TestAuthenticate_NoUsernameEnumerationViaSkippedVerification(t *testing.T) {
	const runs = 20
	cases := []struct {
		name       string
		username   string
		createUser bool
	}{
		{name: "existing user with wrong password", username: "timing-present", createUser: true},
		{name: "nonexistent user", username: "timing-absent", createUser: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			if tc.createUser {
				e.createUser(t, tc.username)
			}
			verifications := countVerifications(e.accounts.hasher)

			for i := 0; i < runs; i++ {
				_, err := e.accounts.Authenticate(context.Background(), tc.username, "wrong-password-42")
				if !errors.Is(err, ErrInvalidCredentials) {
					t.Fatalf("Authenticate() error = %v, want ErrInvalidCredentials", err)
				}
			}
			if got := verifications.Load(); got != runs {
				t.Errorf("argon2id verifications = %d, want %d: the %q branch skipped the password check",
					got, runs, tc.name)
			}
		})
	}
}

// TestAuthenticate_FailureBehaviourIsIdentical pins the external semantics of the two failure paths:
// both return the same error value with identical text, and the disabled-account branch is still unreachable — this change must not alter any of those properties.
func TestAuthenticate_FailureBehaviourIsIdentical(t *testing.T) {
	e := newTestEnv(t)
	e.createUser(t, "timing-present")

	_, existingErr := e.accounts.Authenticate(context.Background(), "timing-present", "wrong-password-42")
	_, absentErr := e.accounts.Authenticate(context.Background(), "timing-absent", "wrong-password-42")

	if !errors.Is(existingErr, ErrInvalidCredentials) || !errors.Is(absentErr, ErrInvalidCredentials) {
		t.Fatalf("errors = (%v, %v), want both ErrInvalidCredentials", existingErr, absentErr)
	}
	if existingErr.Error() != absentErr.Error() {
		t.Errorf("error texts differ: %q vs %q", existingErr.Error(), absentErr.Error())
	}
	if errors.Is(absentErr, ErrUserDisabled) {
		t.Errorf("nonexistent user reaches ErrUserDisabled; a disabled account must stay indistinguishable until the password matches")
	}
}

// TestVerifyDummy_ReusesOneFixedHash asserts that the dummy hash is generated only once and reused:
// rebuilding it on every request would add one full argon2id cost and would instead turn that branch into an amplification surface.
func TestVerifyDummy_ReusesOneFixedHash(t *testing.T) {
	const runs = 20
	h := NewPasswordHasher(fastParams())

	var generations, verifications atomic.Int64
	realRand := h.rand
	h.rand = func(b []byte) error {
		generations.Add(1)
		return realRand(b)
	}
	h.onVerify = func() { verifications.Add(1) }

	for i := 0; i < runs; i++ {
		h.VerifyDummy("wrong-password-42")
	}

	if got := generations.Load(); got != 1 {
		t.Errorf("dummy hash generations = %d, want 1 (the dummy hash must be frozen, not rebuilt per request)", got)
	}
	if got := verifications.Load(); got != runs {
		t.Errorf("dummy verifications = %d, want %d (each call must run argon2id)", got, runs)
	}

	// Equal cost: the dummy hash must carry the same argon2id parameters as a real hash.
	params, _, _, err := decodeHash(h.dummyHash)
	if err != nil {
		t.Fatalf("decodeHash(dummy) error = %v", err)
	}
	want := h.params
	if params.Memory != want.Memory || params.Time != want.Time || params.Threads != want.Threads {
		t.Errorf("dummy params = m=%d,t=%d,p=%d, want m=%d,t=%d,p=%d",
			params.Memory, params.Time, params.Threads, want.Memory, want.Time, want.Threads)
	}
}
