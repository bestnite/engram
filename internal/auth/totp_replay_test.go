package auth

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

// This file holds the acceptance cases for TOTP time-step replay protection. The rule:
// the maximum accepted time step is recorded per user, and the same code may not pass twice within the window.
//
// Time uses fixed time steps instead of the real clock: codes are generated per step and adjacent steps are only 30 seconds apart,
// so a real clock would make "same step" and "adjacent step" uncontrollable in tests (crossing a boundary switches steps).

// stepStart returns the start instant of a time step (a multiple of unix/period).
func stepStart(step int64) time.Time { return time.Unix(step*totpPeriod, 0).UTC() }

// codeAt generates the verification code for the given time step.
func codeAt(t *testing.T, secret string, step int64) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, stepStart(step))
	if err != nil {
		t.Fatalf("GenerateCode(step %d) error = %v", step, err)
	}
	return code
}

// enabledAt completes the binding at the given time step: Begin generates the secret, then Confirm with that step's code.
// Returns the plaintext secret; the service's time is pinned to that step.
func enabledAt(t *testing.T, svc *TOTPService, userID uint64, username string, step int64) string {
	t.Helper()
	ctx := context.Background()
	svc.now = func() time.Time { return stepStart(step) }
	secret, _, err := svc.Begin(ctx, userID, username)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if _, err := svc.Confirm(ctx, userID, codeAt(t, secret, step)); err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	return secret
}

// at pins the service time to a given time step.
func at(svc *TOTPService, step int64) { svc.now = func() time.Time { return stepStart(step) } }

// TestTOTPRejectsReplayWithinSameStep asserts: the same code in the same step can be used only once.
// This is the core regression — before the fix a code observed within the window could be replayed without limit.
func TestTOTPRejectsReplayWithinSameStep(t *testing.T) {
	svc, _, e := newTOTPFixture(t)
	u := e.createUser(t, "totp-replay-same")
	ctx := context.Background()
	secret := enabledAt(t, svc, u.ID, u.Username, 1000)

	at(svc, 1001)
	code := codeAt(t, secret, 1001)
	if _, ok, err := svc.VerifySecondFactor(ctx, u.ID, code); err != nil || !ok {
		t.Fatalf("first use of the step-1001 code = (ok=%v, err=%v), want accepted", ok, err)
	}
	if _, ok, err := svc.VerifySecondFactor(ctx, u.ID, code); err != nil || ok {
		t.Fatalf("replayed step-1001 code = (ok=%v, err=%v), want rejected", ok, err)
	}
}

// TestTOTPConfirmStepIsNotReplayableAtLogin asserts that the code used for enablement confirmation is equally non-replayable at the login entry point:
// confirmation and login follow the same "accept a code" rule, so there is no gap that protects only one of the two paths.
func TestTOTPConfirmStepIsNotReplayableAtLogin(t *testing.T) {
	svc, _, e := newTOTPFixture(t)
	u := e.createUser(t, "totp-replay-confirm")
	ctx := context.Background()
	secret := enabledAt(t, svc, u.ID, u.Username, 1500)

	// The time does not move after confirmed, so the window still holds the same step's code — it was already accepted at confirmation.
	if _, ok, err := svc.VerifySecondFactor(ctx, u.ID, codeAt(t, secret, 1500)); err != nil || ok {
		t.Fatalf("the confirm-step code at login = (ok=%v, err=%v), want rejected", ok, err)
	}
}

// TestTOTPAcceptsConsecutiveNewSteps asserts that replay protection did not tighten into "only one step usable":
// consecutive new steps entering the window still pass one after another.
func TestTOTPAcceptsConsecutiveNewSteps(t *testing.T) {
	svc, _, e := newTOTPFixture(t)
	u := e.createUser(t, "totp-consecutive")
	ctx := context.Background()
	secret := enabledAt(t, svc, u.ID, u.Username, 2000)

	for step := int64(2001); step <= 2003; step++ {
		at(svc, step)
		if _, ok, err := svc.VerifySecondFactor(ctx, u.ID, codeAt(t, secret, step)); err != nil || !ok {
			t.Fatalf("step %d = (ok=%v, err=%v), want accepted: consecutive new steps must stay usable", step, ok, err)
		}
	}
}

// TestTOTPRejectsStepsAtOrBelowTheAcceptedMaximum asserts strict monotonicity:
// codes still inside the ±1 window but not later than the maximum accepted step must all be rejected (window ≠ replay permission).
func TestTOTPRejectsStepsAtOrBelowTheAcceptedMaximum(t *testing.T) {
	svc, _, e := newTOTPFixture(t)
	u := e.createUser(t, "totp-old-step")
	ctx := context.Background()
	secret := enabledAt(t, svc, u.ID, u.Username, 3000)

	// Accept 3002: the maximum step moves up to 3002, while the window at this moment still holds 3001..3003.
	at(svc, 3001)
	if _, ok, err := svc.VerifySecondFactor(ctx, u.ID, codeAt(t, secret, 3002)); err != nil || !ok {
		t.Fatalf("step 3002 = (ok=%v, err=%v), want accepted", ok, err)
	}
	// Both 3002 and 3001 are earlier than (or equal to) the accepted maximum step 3002.
	for _, old := range []int64{3002, 3001} {
		if _, ok, err := svc.VerifySecondFactor(ctx, u.ID, codeAt(t, secret, old)); err != nil || ok {
			t.Fatalf("step %d after 3002 was accepted = (ok=%v, err=%v), want rejected", old, ok, err)
		}
	}
}

// TestTOTPStillRejectsExpiredCode asserts that codes outside the window are still rejected (replay protection does not alter the clock-skew window).
func TestTOTPStillRejectsExpiredCode(t *testing.T) {
	svc, _, e := newTOTPFixture(t)
	u := e.createUser(t, "totp-expired")
	ctx := context.Background()
	secret := enabledAt(t, svc, u.ID, u.Username, 4000)

	// It is now 4005, so the code for 4000 is outside the ±1 window.
	at(svc, 4005)
	if _, ok, err := svc.VerifySecondFactor(ctx, u.ID, codeAt(t, secret, 4000)); err != nil || ok {
		t.Fatalf("expired step-4000 code = (ok=%v, err=%v), want rejected", ok, err)
	}
}

// TestTOTPConcurrentSubmissionOfSameCodeSucceedsOnce asserts that concurrent submission of the same code succeeds only once.
//
// The guarantee comes from the store's conditional update (last_used_step IS NULL OR last_used_step < step):
// under concurrency only one request can turn RowsAffected into 1. If SQLite refuses a write due to serialization,
// that request returns an error rather than "success", so asserting "exactly one ok" holds.
func TestTOTPConcurrentSubmissionOfSameCodeSucceedsOnce(t *testing.T) {
	svc, _, e := newTOTPFixture(t)
	u := e.createUser(t, "totp-concurrent")
	secret := enabledAt(t, svc, u.ID, u.Username, 5000)
	at(svc, 5001)
	code := codeAt(t, secret, 5001)

	const n = 2
	oks := make([]bool, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, ok, err := svc.VerifySecondFactor(context.Background(), u.ID, code)
			oks[i], errs[i] = ok, err
		}(i)
	}
	close(start)
	wg.Wait()

	succeeded := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("concurrent submission %d failed with a database error: %v", i, errs[i])
		}
		if oks[i] {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("%d of %d concurrent submissions of the same code succeeded, want exactly 1", succeeded, n)
	}
}
