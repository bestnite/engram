package auth

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

// 本文件是 F19（TOTP 时间步防重放）的验收用例。设计见 DESIGN.md §13 第 3 项：
// 每用户记录已接受的最大时间步，窗口内同一码不得二次通过。
//
// 时间用固定时间步而不是真实时钟：验证码按步生成、步与步之间只差 30 秒，
// 真实时钟会让「同一步」与「相邻步」在测试里不可控（跨边界即换步）。

// stepStart 返回某个时间步的起点时刻（unix/period 的整数倍）。
func stepStart(step int64) time.Time { return time.Unix(step*totpPeriod, 0).UTC() }

// codeAt 用给定时间步生成验证码。
func codeAt(t *testing.T, secret string, step int64) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, stepStart(step))
	if err != nil {
		t.Fatalf("GenerateCode(step %d) error = %v", step, err)
	}
	return code
}

// enabledAt 在给定时间步走完绑定：Begin 生成 secret，再用该步的验证码 Confirm。
// 返回明文 secret；服务的时间被钉死在该步。
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

// at 把服务时间钉在某个时间步。
func at(svc *TOTPService, step int64) { svc.now = func() time.Time { return stepStart(step) } }

// TestTOTPRejectsReplayWithinSameStep 断言：同一步的同一个码只能用一次。
// 这是 F19 的核心回归——修复前窗口内被观测到的码可无限重放。
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

// TestTOTPConfirmStepIsNotReplayableAtLogin 断言开通确认所用的码在登录入口同样不可重放：
// 确认与登录是同一个「接受验证码」的规则，不存在只保护其中一条路径的缺口。
func TestTOTPConfirmStepIsNotReplayableAtLogin(t *testing.T) {
	svc, _, e := newTOTPFixture(t)
	u := e.createUser(t, "totp-replay-confirm")
	ctx := context.Background()
	secret := enabledAt(t, svc, u.ID, u.Username, 1500)

	// confirmed 后时间不变，窗口内仍是同一步的码——它已经在确认时被接受过。
	if _, ok, err := svc.VerifySecondFactor(ctx, u.ID, codeAt(t, secret, 1500)); err != nil || ok {
		t.Fatalf("the confirm-step code at login = (ok=%v, err=%v), want rejected", ok, err)
	}
}

// TestTOTPAcceptsConsecutiveNewSteps 断言防重放没有收紧成「只能用一步」：
// 进入窗口的相邻新步仍可依次通过。
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

// TestTOTPRejectsStepsAtOrBelowTheAcceptedMaximum 断言严格单调：
// 仍在 ±1 窗口内、但不晚于已接受最大步的码必须一律拒绝（窗口 ≠ 重放许可）。
func TestTOTPRejectsStepsAtOrBelowTheAcceptedMaximum(t *testing.T) {
	svc, _, e := newTOTPFixture(t)
	u := e.createUser(t, "totp-old-step")
	ctx := context.Background()
	secret := enabledAt(t, svc, u.ID, u.Username, 3000)

	// 接受 3002：最大步前移到 3002，而此刻窗口仍是 3001..3003。
	at(svc, 3001)
	if _, ok, err := svc.VerifySecondFactor(ctx, u.ID, codeAt(t, secret, 3002)); err != nil || !ok {
		t.Fatalf("step 3002 = (ok=%v, err=%v), want accepted", ok, err)
	}
	// 3002 与 3001 都早于（或等于）已接受的最大步 3002。
	for _, old := range []int64{3002, 3001} {
		if _, ok, err := svc.VerifySecondFactor(ctx, u.ID, codeAt(t, secret, old)); err != nil || ok {
			t.Fatalf("step %d after 3002 was accepted = (ok=%v, err=%v), want rejected", old, ok, err)
		}
	}
}

// TestTOTPStillRejectsExpiredCode 断言超出窗口的码照旧被拒（防重放不改变时钟漂移窗口）。
func TestTOTPStillRejectsExpiredCode(t *testing.T) {
	svc, _, e := newTOTPFixture(t)
	u := e.createUser(t, "totp-expired")
	ctx := context.Background()
	secret := enabledAt(t, svc, u.ID, u.Username, 4000)

	// 现在在 4005，4000 的码超出 ±1 窗口。
	at(svc, 4005)
	if _, ok, err := svc.VerifySecondFactor(ctx, u.ID, codeAt(t, secret, 4000)); err != nil || ok {
		t.Fatalf("expired step-4000 code = (ok=%v, err=%v), want rejected", ok, err)
	}
}

// TestTOTPConcurrentSubmissionOfSameCodeSucceedsOnce 断言并发提交同一码只有一个成功。
//
// 保证来自 store 的条件更新（last_used_step IS NULL OR last_used_step < step）：
// 并发下只有一个请求能把 RowsAffected 变成 1。SQLite 若因串行化拒绝某个写，
// 该请求会返回错误而不是「成功」，因此断言写成「恰好一个 ok」是成立的。
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
