package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
)

// assertLockedOut asserts the response is a lockout refusal: 429, the stable code rate_limited, and a positive integer Retry-After.
func assertLockedOut(t *testing.T, step string, code int, header http.Header, body []byte) {
	t.Helper()
	if code != http.StatusTooManyRequests {
		t.Fatalf("%s: status = %d, want 429 (body %s)", step, code, snippet(string(body)))
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil || env.Error.Code != api.CodeRateLimited {
		t.Fatalf("%s: body = %s, want code %s", step, snippet(string(body)), api.CodeRateLimited)
	}
	if secs, err := strconv.Atoi(header.Get("Retry-After")); err != nil || secs < 1 {
		t.Fatalf("%s: Retry-After = %q, want a positive number of seconds", step, header.Get("Retry-After"))
	}
}

// TestLoginLocksAfterRepeatedFailures is the negative case: once one account hits the threshold with consecutive wrong entries, even the correct password is
// refused outright (no authentication attempted) until the window passes — otherwise an attacker could bypass the incremental delay just by going concurrent.
func TestLoginLocksAfterRepeatedFailures(t *testing.T) {
	srv, db := newAuthServer(t)
	_ = createTOTPAdmin(t, srv, db)
	for i := 0; i < auth.DefaultMaxAccountFailures; i++ {
		rec, _, _ := totpJSONLogin(t, srv, "admin", "wrong-password")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong password #%d = %d, want 401 (body %s)", i+1, rec.Code, snippet(rec.Body.String()))
		}
	}
	rec, _, _ := totpJSONLogin(t, srv, "admin", "Sup3rSecret!")
	assertLockedOut(t, "correct password after the threshold", rec.Code, rec.Header(), rec.Body.Bytes())
}

// TestTOTPSecondStepLocksAfterRepeatedFailures asserts the second step shares the lockout counter with the first: a passed first factor
// does not count as a failure (the slot is returned), while wrong second-step entries accumulate to the threshold and then even valid credentials are refused outright — the 6-digit code therefore
// cannot be brute-forced.
func TestTOTPSecondStepLocksAfterRepeatedFailures(t *testing.T) {
	srv, db := newAuthServer(t)
	admin := createTOTPAdmin(t, srv, db)
	_, _ = enableTOTPFor(t, srv, admin)

	pending := startSecondStep(t, srv)
	for i := 0; i < auth.DefaultMaxAccountFailures; i++ {
		rec := postTOTPJSON(t, srv, "000000", nil, pending, testDoubleSubmitToken)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong code #%d = %d, want 401 (body %s)", i+1, rec.Code, snippet(rec.Body.String()))
		}
	}
	rec := postTOTPJSON(t, srv, "000000", nil, pending, testDoubleSubmitToken)
	assertLockedOut(t, "second step after the threshold", rec.Code, rec.Header(), rec.Body.Bytes())
}
