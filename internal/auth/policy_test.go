package auth

import (
	"errors"
	"testing"
)

// TestParseRegistrationPolicy 断言策略归一化：合法值保留，空值与未知值回落到 closed。
func TestParseRegistrationPolicy(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"open", PolicyOpen},
		{"OPEN", PolicyOpen},
		{" open ", PolicyOpen},
		{"invite", PolicyInvite},
		{"closed", PolicyClosed},
		{"", PolicyClosed},
		{"garbage", PolicyClosed},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			if got := ParseRegistrationPolicy(tc.raw); got != tc.want {
				t.Errorf("ParseRegistrationPolicy(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestParseEmailAllowlist 覆盖 JSON 数组与分隔符两种写法，以及大小写归一化。
func TestParseEmailAllowlist(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", "", nil},
		{"json array", `["example.com","example.org"]`, []string{"example.com", "example.org"}},
		{"comma separated", "example.com, example.org", []string{"example.com", "example.org"}},
		{"whitespace separated", "example.com\ncorp.example.org", []string{"example.com", "corp.example.org"}},
		{"uppercase", "Example.COM", []string{"example.com"}},
		{"malformed json falls back", `[not json`, []string{"[not", "json"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseEmailAllowlist(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("ParseEmailAllowlist(%q) = %v, want %v", tc.raw, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("ParseEmailAllowlist(%q)[%d] = %q, want %q", tc.raw, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestEmailDomainAllowed 覆盖空白名单放行、命中、未命中与大小写。
func TestEmailDomainAllowed(t *testing.T) {
	cases := []struct {
		name      string
		email     string
		allowlist []string
		want      bool
	}{
		{"empty allowlist allows all", "anyone@example.net", nil, true},
		{"exact match", "alice@example.com", []string{"example.com"}, true},
		{"case insensitive", "alice@Example.COM", []string{"example.com"}, true},
		{"no match", "alice@example.net", []string{"example.com"}, false},
		{"allowlist is not a suffix match", "alice@example.org", []string{"example.com"}, false},
		{"malformed email", "no-at-sign", []string{"example.com"}, false},
		{"trailing at", "alice@", []string{"example.com"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EmailDomainAllowed(tc.email, tc.allowlist); got != tc.want {
				t.Errorf("EmailDomainAllowed(%q, %v) = %v, want %v", tc.email, tc.allowlist, got, tc.want)
			}
		})
	}
}

// TestDecideRegistration 是 M1-6 的验收矩阵：每一种策略 × 白名单的放行/拒绝组合。
func TestDecideRegistration(t *testing.T) {
	cases := []struct {
		name      string
		policy    string
		email     string
		allowlist []string
		wantErr   error
	}{
		// open：白名单为空 → 全部放行。
		{name: "open without allowlist", policy: PolicyOpen, email: "alice@example.com"},
		// open + 白名单：命中放行，未命中拒绝。
		{name: "open allowlist match", policy: PolicyOpen, email: "alice@example.com", allowlist: []string{"example.com"}},
		{name: "open allowlist denial", policy: PolicyOpen, email: "alice@example.net", allowlist: []string{"example.com"}, wantErr: ErrEmailDomainNotAllowed},
		// invite：无邀请一律要求邀请（邀请分支在 web 处理器里另行放行）。
		{name: "invite requires invite", policy: PolicyInvite, email: "alice@example.com", wantErr: ErrInviteRequired},
		{name: "invite ignores allowlist", policy: PolicyInvite, email: "alice@example.com", allowlist: []string{"example.com"}, wantErr: ErrInviteRequired},
		// closed：无论邮箱与白名单都拒绝。
		{name: "closed rejects", policy: PolicyClosed, email: "alice@example.com", wantErr: ErrRegistrationClosed},
		{name: "closed rejects allowlisted", policy: PolicyClosed, email: "alice@example.com", allowlist: []string{"example.com"}, wantErr: ErrRegistrationClosed},
		// 未知/空策略按 closed 处理（安全默认）。
		{name: "unknown policy behaves as closed", policy: "weird", email: "alice@example.com", wantErr: ErrRegistrationClosed},
		{name: "empty policy behaves as closed", policy: "", email: "alice@example.com", wantErr: ErrRegistrationClosed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := DecideRegistration(tc.policy, tc.email, tc.allowlist)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("DecideRegistration(%q, %q, %v) = %v, want nil", tc.policy, tc.email, tc.allowlist, err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("DecideRegistration(%q, %q, %v) = %v, want %v", tc.policy, tc.email, tc.allowlist, err, tc.wantErr)
			}
		})
	}
}
