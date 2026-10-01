package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"example.com/flashcard/internal/store"
)

// 本文件是 M1-13 要求的鉴权反面用例汇总：把 CSRF、注册策略、身份绑定、会话作废、
// 限流五类“本该被拒绝”的场景集中到一处，方便一处审阅全部拒绝路径。
// 每类一个命名测试；细粒度正向用例仍在各自 *_test.go 中，避免重复。

// TestAuthSuite_CSRF_NegativeCases 汇总 CSRF 的三种拒绝：无 token、错 token、无会话。
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

// TestAuthSuite_RegistrationPolicy_NegativeCases 汇总自助注册被拒的三条路径。
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

// TestAuthSuite_IdentityBinding_NegativeCases 汇总绑定流程必须拒绝/不得自动绑定的场景：
// closed 策略无匹配 → 拒绝；未验证邮箱撞上既有账号 → 拒绝（绝不自动绑定）。
func TestAuthSuite_IdentityBinding_NegativeCases(t *testing.T) {
	matched := &store.User{ID: 3, Email: "alice@example.com"}

	closed := DecideIdentityLink(OAuthProfile{
		Provider: "issuer.example.com", Subject: "sub-1", Email: "nobody@example.com", EmailVerified: true,
	}, nil, nil, PolicyClosed)
	if closed.Kind != LinkDecisionDeny || !errors.Is(closed.Err, ErrIdentityLinkDenied) {
		t.Errorf("closed policy = (%q, %v), want deny/%v", closed.Kind, closed.Err, ErrIdentityLinkDenied)
	}

	// 未验证邮箱即便命中既有账号，也必须走拒绝而不是自动绑定。
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

// TestAuthSuite_SessionInvalidation_NegativeCases 汇总会话作废的三条服务端路径：
// 禁用用户、改密码、登出之后，旧 cookie 必须被拒。
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

// TestAuthSuite_RateLimiting_NegativeCases 汇总限流的三种减速/归零行为：
// 连续失败延迟递增、同 IP 换账号仍受限、窗口过期后计数清零。
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
