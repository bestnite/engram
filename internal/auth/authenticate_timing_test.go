package auth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

// countVerifications 把 onVerify 钩子接到一个计数器上，作为“是否真的执行了 argon2id”的可观测代理。
//
// 为什么不比较墙钟毫秒：那类断言在共享 CI 上必然抖动，只会制造不可靠的红；
// 代价相同由构造保证（dummy 哈希用同一哈希器的参数生成），测试只验证两条路径都执行了校验。
func countVerifications(h *PasswordHasher) *atomic.Int64 {
	var n atomic.Int64
	h.onVerify = func() { n.Add(1) }
	return &n
}

// TestAuthenticate_NoUsernameEnumerationViaSkippedVerification 断言登录校验对
// “存在用户 + 错密码”与“不存在的用户”都执行一次 argon2id。
// 现状下不存在用户的分支直接返回，不消耗任何哈希计算，因而可用响应时间枚举用户名。
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

// TestAuthenticate_FailureBehaviourIsIdentical 锁定两条失败路径的对外语义：
// 都返回同一个错误值、文本一致，且禁用账号分支仍不可达 —— 本次改动不得改变这些性质。
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

// TestVerifyDummy_ReusesOneFixedHash 断言 dummy 哈希只生成一次并被重复使用：
// 每次请求重建会多出一次完整 argon2id 成本，反倒把该分支变成放大面。
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

	// 代价相同：dummy 哈希必须携带与真实哈希一致的 argon2id 参数。
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
