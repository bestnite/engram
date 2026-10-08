package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 SPA 登录第二步（internal/web/totp_login.go）的端到端证据：
// 走真实路由 + 真实 SQLite，复用 auth.TOTPService，断言的状态与 SSR 表单路径一致。
//
// 覆盖点：pending 查询的状态语义、双提交 CSRF 强制、凭据缺失/过期、验证码错误（含审计）、
// 恢复码消费、通过后签发会话与会话 cookie、以及 /spa/login/totp 外壳。
//
// 时间步注意：Confirm 会记录「已接受的最大时间步」（与登录同规），因此启用后必须用
// 下一步的码登录——夹具统一用 time.Now().Add(totpPeriod*time.Second)（既有 TOTP 测试同法）。

// totpJSONLogin 走 SPA 的 JSON 登录（POST /api/v1/auth/login），返回响应与前后的 cookie。
// 第一因素通过但账号启用 TOTP 时，服务端下发第二步凭据 cookie 并返回 requires_totp=true。
func totpJSONLogin(t *testing.T, srv *Server, username, password string) (*httptest.ResponseRecorder, *http.Cookie, *http.Cookie) {
	t.Helper()
	initRec := get(t, srv, "/api/v1/auth/session", nil)
	doubleCookie := findCookie(initRec, auth.CSRFDoubleSubmitCookieName)
	if doubleCookie == nil {
		t.Fatal("GET /api/v1/auth/session did not set the double-submit cookie")
	}
	var initRes struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(initRec.Body.Bytes(), &initRes); err != nil {
		t.Fatalf("unmarshal session init: %v", err)
	}
	rec := postJSON(srv, "/api/v1/auth/login", map[string]string{
		"username": username, "password": password,
	}, []*http.Cookie{doubleCookie}, map[string]string{auth.CSRFHeaderName: initRes.CSRFToken})
	return rec, doubleCookie, findCookie(rec, totpPendingCookieName)
}

// postTOTPJSON 提交登录第二步。双提交 CSRF 要求 cookie 与镜像头取值一致：非空 csrf 时两者
// 都由它派生；csrf 为空时原样发起（用于覆盖缺 token 的拒绝路径）。
func postTOTPJSON(t *testing.T, srv *Server, code string, cookies []*http.Cookie, csrfCookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	headers := map[string]string{}
	all := append([]*http.Cookie{}, cookies...)
	if csrf != "" {
		headers[auth.CSRFHeaderName] = csrf
		all = append(all, &http.Cookie{Name: auth.CSRFDoubleSubmitCookieName, Value: csrf})
	}
	if csrfCookie != nil {
		all = append(all, csrfCookie)
	}
	return postJSON(srv, "/api/v1/auth/totp", map[string]string{"code": code}, all, headers)
}

// TestTOTPLoginPendingReflectsChallengeState 断言 pending 只反映「是否持有有效的第二步凭据」，
// 且匿名请求永远得到 false（不泄露任何账号是否启用 TOTP）。
func TestTOTPLoginPendingReflectsChallengeState(t *testing.T) {
	srv, db := newAuthServer(t)
	_ = createTOTPAdmin(t, srv, db)
	cookies, csrf := totpLogin(t, srv, db)
	_ = totpEnable(t, srv, cookies, csrf)

	// 匿名：没有凭据，且响应体里不出现任何 TOTP 材料。
	anon := getWithCookies(t, srv, "/api/v1/auth/totp", nil)
	if anon.Code != http.StatusOK {
		t.Fatalf("anonymous GET pending = %d, want 200 (body %s)", anon.Code, snippet(anon.Body.String()))
	}
	assertNoTOTPSecret(t, anon.Body.String())
	var anonRes struct {
		Pending bool `json:"pending"`
	}
	if err := json.Unmarshal(anon.Body.Bytes(), &anonRes); err != nil {
		t.Fatal(err)
	}
	if anonRes.Pending {
		t.Fatalf("anonymous pending = true, want false")
	}

	// 第一因素通过后才下发凭据：此后 pending=true。
	loginRec, _, pendingCookie := totpJSONLogin(t, srv, "admin", "Sup3rSecret!")
	if loginRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/login = %d, want 200 (body %s)", loginRec.Code, snippet(loginRec.Body.String()))
	}
	if !strings.Contains(loginRec.Body.String(), "requires_totp") {
		t.Fatalf("login for a TOTP account must ask for the second factor: %s", snippet(loginRec.Body.String()))
	}
	if pendingCookie == nil {
		t.Fatal("login did not set the second-factor pending cookie")
	}
	if findCookie(loginRec, srv.sessions.CookieName()) != nil {
		t.Error("login must not issue a session cookie before the second factor")
	}

	withChallenge := getWithCookies(t, srv, "/api/v1/auth/totp", []*http.Cookie{pendingCookie})
	var withRes struct {
		Pending bool `json:"pending"`
	}
	if err := json.Unmarshal(withChallenge.Body.Bytes(), &withRes); err != nil {
		t.Fatal(err)
	}
	if !withRes.Pending {
		t.Fatalf("pending with a valid challenge cookie = false, want true")
	}
	assertNoTOTPSecret(t, withChallenge.Body.String())
}

// TestTOTPLoginSecondStepIssuesSession 覆盖：错误码被拒且留痕、正确码签发会话、
// 凭据被清理、会话 cookie 立即可用。
func TestTOTPLoginSecondStepIssuesSession(t *testing.T) {
	srv, db := newAuthServer(t)
	_ = createTOTPAdmin(t, srv, db)
	cookies, csrf := totpLogin(t, srv, db)
	secret := totpEnable(t, srv, cookies, csrf)

	_, _, pendingCookie := totpJSONLogin(t, srv, "admin", "Sup3rSecret!")
	if pendingCookie == nil {
		t.Fatal("login did not set the second-factor pending cookie")
	}

	// 错误码：401 + 稳定 code，且写一条 totp.verify_failed 审计。
	bad := postTOTPJSON(t, srv, "000000", nil, pendingCookie, testDoubleSubmitToken)
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("POST with a wrong code = %d, want 401 (body %s)", bad.Code, snippet(bad.Body.String()))
	}
	if !strings.Contains(bad.Body.String(), "totp_invalid") {
		t.Errorf("wrong code must report totp_invalid, got %s", snippet(bad.Body.String()))
	}
	if findCookie(bad, srv.sessions.CookieName()) != nil {
		t.Error("a rejected second factor must not issue a session cookie")
	}
	audits := store.NewAuditStore(db)
	if n, err := audits.CountByAction(context.Background(), store.ActionTOTPVerifyFailed); err != nil || n != 1 {
		t.Fatalf("totp.verify_failed audit = (%d, %v), want 1", n, err)
	}

	// 正确码：必须用下一步的码（Confirm 已消费当前时间步）。
	code, err := totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("GenerateCode() error = %v", err)
	}
	ok := postTOTPJSON(t, srv, code, nil, pendingCookie, testDoubleSubmitToken)
	if ok.Code != http.StatusOK {
		t.Fatalf("POST with a valid code = %d, want 200 (body %s)", ok.Code, snippet(ok.Body.String()))
	}
	var okRes struct {
		Authenticated bool `json:"authenticated"`
		User          struct {
			Username string `json:"username"`
		} `json:"user"`
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(ok.Body.Bytes(), &okRes); err != nil {
		t.Fatal(err)
	}
	if !okRes.Authenticated || okRes.User.Username != "admin" || okRes.CSRFToken == "" {
		t.Fatalf("second-step success response = %+v, want an authenticated admin with a CSRF token", okRes)
	}
	sessCookie := findCookie(ok, srv.sessions.CookieName())
	if sessCookie == nil {
		t.Fatal("a passing second factor must issue a session cookie")
	}

	// 会话立即可用。
	me := getWithCookies(t, srv, "/api/v1/auth/session", []*http.Cookie{sessCookie})
	var meRes struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &meRes); err != nil {
		t.Fatal(err)
	}
	if !meRes.Authenticated {
		t.Errorf("session after the second factor is not authenticated: %s", snippet(me.Body.String()))
	}

	// 第二步凭据被清理：成功响应把该 cookie 置空过期（浏览器据此丢弃）。
	cleared := findCookie(ok, totpPendingCookieName)
	if cleared == nil || cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Errorf("successful login must clear %s, got %+v", totpPendingCookieName, cleared)
	}

	// 登录成功审计带上 second_factor=totp（与 SSR 同口径）。
	if n, err := audits.CountByAction(context.Background(), store.ActionUserLoginSucceeded); err != nil || n != 2 {
		t.Fatalf("user.login_succeeded audit = (%d, %v), want 2 (initial setup login + second step)", n, err)
	}
}

// TestTOTPLoginSecondStepGuards 覆盖两条拒绝路径：缺少双提交 token、以及没有第二步凭据。
func TestTOTPLoginSecondStepGuards(t *testing.T) {
	srv, db := newAuthServer(t)
	_ = createTOTPAdmin(t, srv, db)
	cookies, csrf := totpLogin(t, srv, db)
	_ = totpEnable(t, srv, cookies, csrf)

	_, _, pendingCookie := totpJSONLogin(t, srv, "admin", "Sup3rSecret!")
	if pendingCookie == nil {
		t.Fatal("login did not set the second-factor pending cookie")
	}

	// 缺 CSRF：双提交中间件必须拦截，且不得签发会话。
	noCSRF := postTOTPJSON(t, srv, "123456", nil, nil, "")
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("POST without the double-submit token = %d, want 403 (body %s)", noCSRF.Code, snippet(noCSRF.Body.String()))
	}
	if findCookie(noCSRF, srv.sessions.CookieName()) != nil {
		t.Error("a CSRF-rejected request must not issue a session cookie")
	}

	// 没有第二步凭据：稳定 code 提示重新登录，且不签发会话。
	noChallenge := postTOTPJSON(t, srv, "123456", nil, nil, testDoubleSubmitToken)
	if noChallenge.Code != http.StatusUnauthorized {
		t.Fatalf("POST without a pending challenge = %d, want 401 (body %s)", noChallenge.Code, snippet(noChallenge.Body.String()))
	}
	if !strings.Contains(noChallenge.Body.String(), "totp_challenge_expired") {
		t.Errorf("missing challenge must report totp_challenge_expired, got %s", snippet(noChallenge.Body.String()))
	}
	if findCookie(noChallenge, srv.sessions.CookieName()) != nil {
		t.Error("a request without a challenge must not issue a session cookie")
	}
}

// TestTOTPLoginShellServesAppShell 断言 GET /spa/login/totp 返回应用壳并下发双提交 cookie，
// 且第二步不再有 SSR 表单端点：POST /login/totp 未注册，落到 NoRoute（非 GET 一律 404）。
func TestTOTPLoginShellServesAppShell(t *testing.T) {
	srv, _ := newAuthServer(t)
	rec := get(t, srv, "/spa/login/totp", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /spa/login/totp = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET /spa/login/totp Content-Type = %q, want text/html", ct)
	}
	if findCookie(rec, auth.CSRFDoubleSubmitCookieName) == nil {
		t.Errorf("GET /spa/login/totp did not set the %s cookie", auth.CSRFDoubleSubmitCookieName)
	}
	if findCookie(rec, srv.sessions.CookieName()) != nil {
		t.Error("GET /spa/login/totp must not issue a session cookie")
	}
	// SSR 的第二步表单端点已随页面层移除：POST /login/totp 不再是注册路由。
	ssrStep := postJSON(srv, "/login/totp", map[string]string{"code": "123456"}, nil, nil)
	if ssrStep.Code != http.StatusNotFound {
		t.Errorf("POST /login/totp = %d, want 404 after the SSR endpoint was removed", ssrStep.Code)
	}
}
