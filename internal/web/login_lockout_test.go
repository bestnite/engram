package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
)

// assertLockedOut 断言响应是锁定拒绝：429、稳定 code rate_limited、带正整数 Retry-After。
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

// TestLoginLocksAfterRepeatedFailures 是反面用例：同一账号连续输错达到阈值后，连正确密码也被
// 直接拒绝（不做认证），直到窗口过去——否则攻击者只需并发就能绕过递增延迟。
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

// TestTOTPSecondStepLocksAfterRepeatedFailures 断言第二步与第一步共用锁定计数：第一因素通过
// 不计失败（归还占位），而第二步输错累计到阈值后，持有有效凭据也被直接拒绝——6 位验证码因此
// 无法被穷举。
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
