package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// This file gathers the negative auth cases: it collects the five kinds of "should have been rejected" scenario — CSRF, registration policy, identity linking, session revocation
// and rate limiting — in one place, so every rejection path can be reviewed together.
// One named test per kind; the fine-grained positive cases stay in their own *_test.go files to avoid duplication.

// TestAuthSuite_CSRF_NegativeCases gathers the three CSRF rejections: no token, wrong token, no session.
func TestAuthSuite_CSRF_NegativeCases(t *testing.T) {
	cases := []struct {
		name     string
		withUser bool
		method   string
		path     string
		headers  map[string]string
	}{
		{name: "missing token", withUser: true, method: http.MethodPost, path: "/echo"},
		{name: "wrong token", withUser: true, method: http.MethodPost, path: "/echo", headers: map[string]string{CSRFHeaderName: "not-the-token"}},
		{name: "no session", withUser: false, method: http.MethodPost, path: "/echo", headers: map[string]string{CSRFHeaderName: "anything"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			cookie := ""
			if tc.withUser {
				u := e.createUser(t, "csrf-negative")
				cookie, _ = e.startSession(t, u.ID)
			}
			rec := doOn(csrfRouter(e), e.manager.CookieName(), tc.method, tc.path, cookie, tc.headers)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s status = %d, want 403", tc.name, rec.Code)
			}
		})
	}
}

// TestAuthSuite_RegistrationPolicy_NegativeCases gathers the three paths on which self-service registration is rejected.
func TestAuthSuite_RegistrationPolicy_NegativeCases(t *testing.T) {
	cases := []struct {
		name      string
		policy    string
		email     string
		allowlist []string
		wantErr   error
	}{
		{name: "closed rejects everyone", policy: PolicyClosed, email: "alice@example.com", wantErr: ErrRegistrationClosed},
		{name: "invite without token", policy: PolicyInvite, email: "alice@example.com", wantErr: ErrInviteRequired},
		{name: "open allowlist denial", policy: PolicyOpen, email: "alice@example.net", allowlist: []string{"example.com"}, wantErr: ErrEmailDomainNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := DecideRegistration(tc.policy, tc.email, tc.allowlist)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("DecideRegistration(%q, %q, %v) = %v, want %v", tc.policy, tc.email, tc.allowlist, err, tc.wantErr)
			}
		})
	}
}

// TestAuthSuite_IdentityBinding_NegativeCases gathers the scenarios where the linking flow must reject or must not auto-link:
// closed policy with no match → reject; an unverified email colliding with an existing account → reject (never auto-link).
func TestAuthSuite_IdentityBinding_NegativeCases(t *testing.T) {
	matched := &store.User{ID: 3, Email: "alice@example.com"}

	closed := DecideIdentityLink(OAuthProfile{
		Provider: "issuer.example.com", Subject: "sub-1", Email: "nobody@example.com", EmailVerified: true,
	}, nil, nil, PolicyClosed)
	if closed.Kind != LinkDecisionDeny || !errors.Is(closed.Err, ErrIdentityLinkDenied) {
		t.Errorf("closed policy = (%q, %v), want deny/%v", closed.Kind, closed.Err, ErrIdentityLinkDenied)
	}

	// An unverified email, even when it hits an existing account, must go down the rejection path rather than auto-linking.
	unverified := DecideIdentityLink(OAuthProfile{
		Provider: "issuer.example.com", Subject: "sub-2", Email: "alice@example.com", EmailVerified: false,
	}, nil, matched, PolicyOpen)
	if unverified.Kind != LinkDecisionDeny {
		t.Errorf("unverified matching email = %q, want %q (must never auto-link)", unverified.Kind, LinkDecisionDeny)
	}
	if unverified.Kind == LinkDecisionAutoLink || unverified.UserID != 0 {
		t.Errorf("unverified email bound to user %d, want no binding", unverified.UserID)
	}
}

// TestAuthSuite_SessionInvalidation_NegativeCases gathers the three server-side session revocation paths:
// after disabling the user, changing the password or logging out, the old cookie must be rejected.
func TestAuthSuite_SessionInvalidation_NegativeCases(t *testing.T) {
	cases := []struct {
		name   string
		revoke func(t *testing.T, e *testEnv, u *store.User)
	}{
		{
			name: "disabled user",
			revoke: func(t *testing.T, e *testEnv, u *store.User) {
				if err := e.accounts.DisableUser(context.Background(), u.ID); err != nil {
					t.Fatalf("DisableUser() error = %v", err)
				}
			},
		},
		{
			name: "password change",
			revoke: func(t *testing.T, e *testEnv, u *store.User) {
				if err := e.accounts.ChangePassword(context.Background(), u.ID, "Tr0ub4dor&3", "N3w-Str0ng-Pass"); err != nil {
					t.Fatalf("ChangePassword() error = %v", err)
				}
			},
		},
		{
			name: "logout",
			revoke: func(t *testing.T, e *testEnv, u *store.User) {
				if err := e.manager.RevokeAllForUser(context.Background(), u.ID); err != nil {
					t.Fatalf("RevokeAllForUser() error = %v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			u := e.createUser(t, "session-negative")
			cookie, _ := e.startSession(t, u.ID)
			if rec := e.do(t, http.MethodGet, "/me", cookie, nil); rec.Code != http.StatusOK {
				t.Fatalf("pre-revoke /me status = %d, want 200", rec.Code)
			}
			tc.revoke(t, e, u)
			if rec := e.do(t, http.MethodGet, "/me", cookie, nil); rec.Code != http.StatusUnauthorized {
				t.Errorf("post-revoke /me status = %d, want 401", rec.Code)
			}
		})
	}
}

// TestAuthSuite_RateLimiting_NegativeCases gathers the three slowdown/reset behaviors of rate limiting:
// the delay grows with consecutive failures, a different account on the same IP is still limited, and the count clears after the window expires.
func TestAuthSuite_RateLimiting_NegativeCases(t *testing.T) {
	ctx := context.Background()

	t.Run("repeated failures grow the delay", func(t *testing.T) {
		rec := &sleepRecorder{}
		lim := NewLoginLimiter(LimiterConfig{
			BaseDelay: 10 * time.Millisecond, MaxDelay: time.Second, Window: time.Hour, Sleep: rec.sleep,
		})
		var prev time.Duration
		for i := 0; i < 4; i++ {
			lim.RecordFailure("alice", "127.0.0.1")
			d, err := lim.Wait(ctx, "alice", "127.0.0.1")
			if err != nil || d <= prev {
				t.Fatalf("delay after failure %d = (%v, %v), want > %v", i+1, d, err, prev)
			}
			prev = d
		}
	})

	t.Run("same IP throttles a different account", func(t *testing.T) {
		lim := NewLoginLimiter(LimiterConfig{
			BaseDelay: 10 * time.Millisecond, Window: time.Hour, Sleep: func(context.Context, time.Duration) error { return nil },
		})
		lim.RecordFailure("alice", "127.0.0.2")
		if d, err := lim.Wait(ctx, "bob", "127.0.0.2"); err != nil || d <= 0 {
			t.Fatalf("Wait() for a different account on the same IP = (%v, %v), want > 0", d, err)
		}
	})

	t.Run("window expiry clears the failures", func(t *testing.T) {
		now := time.Now().UTC()
		lim := NewLoginLimiter(LimiterConfig{
			BaseDelay: 10 * time.Millisecond, Window: time.Minute,
			Now: func() time.Time { return now }, Sleep: func(context.Context, time.Duration) error { return nil },
		})
		lim.RecordFailure("alice", "127.0.0.3")
		if d, _ := lim.Wait(ctx, "alice", "127.0.0.3"); d <= 0 {
			t.Fatalf("Wait() right after a failure = %v, want > 0", d)
		}
		now = now.Add(2 * time.Minute)
		if d, err := lim.Wait(ctx, "alice", "127.0.0.3"); err != nil || d != 0 {
			t.Fatalf("Wait() after the window expired = (%v, %v), want (0, nil)", d, err)
		}
	})
}
