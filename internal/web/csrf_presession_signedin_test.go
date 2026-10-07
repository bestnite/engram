package web

import (
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
)

// 本文件覆盖上一轮修复的用户可见形态：**已登录**用户点邮件里的链接（验证邮箱、确认改邮箱、
// 退订）或走忘记/重置密码时，CSRF 层必须放行。
//
// 修复前这些请求必然 403 csrf_failed：这类端点的契约是「匿名也能调」，因此只比「双提交 cookie
// == 请求值」，而已登录浏览器手里只有会话绑定 token。现在按「本次请求有没有有效会话」分档。

// TestPreSessionCSRFAcceptsSessionTokenForEveryPreSessionEndpoint 逐一覆盖受影响的端点：
// 已登录 + 会话绑定 token 不得被判为 csrf_failed。
//
// 断言只针对 CSRF 层：handler 自己对 email/token 的判定（比如令牌无效）是它自己的事，
// 所以这里只要求「不是 csrf_failed」。
func TestPreSessionCSRFAcceptsSessionTokenForEveryPreSessionEndpoint(t *testing.T) {
	srv, db := newAuthServer(t)
	seedAdminUser(t, srv)
	cookies, sessionCSRF := loginJSON(t, srv, db, "admin", "Sup3rSecret!")

	cases := []struct {
		path string
		body map[string]string
	}{
		{path: "/api/v1/auth/forgot-password", body: map[string]string{"email": "admin@example.com"}},
		{path: "/api/v1/auth/reset-password", body: map[string]string{"token": "irrelevant", "password": "N3wSup3rSecret!"}},
		{path: "/api/v1/auth/verify-email", body: map[string]string{"token": "irrelevant"}},
		{path: "/api/v1/auth/confirm-email-change", body: map[string]string{"token": "irrelevant"}},
		{path: "/api/v1/unsubscribe", body: map[string]string{"token": "irrelevant"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rec := postJSON(srv, tc.path, tc.body, cookies, map[string]string{auth.CSRFHeaderName: sessionCSRF})
			if rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "csrf_failed") {
				t.Fatalf("signed-in POST %s was rejected by the CSRF layer: %d %s",
					tc.path, rec.Code, snippet(rec.Body.String()))
			}
		})
	}
}

// TestPreSessionCSRFRejectsDoubleSubmitWhileSignedIn 是同一修复的安全面：已登录时不能退回双提交。
// 用应用壳下发的那一对（cookie + 镜像值）提交，必须仍然 403。
func TestPreSessionCSRFRejectsDoubleSubmitWhileSignedIn(t *testing.T) {
	srv, db := newAuthServer(t)
	seedAdminUser(t, srv)
	cookies, _ := loginJSON(t, srv, db, "admin", "Sup3rSecret!")

	// 应用壳给出的双提交对：cookie 与镜像 token 同值，但它不等于会话行里的 token。
	page := get(t, srv, "/login", nil)
	doubleCookie := findCookie(page, auth.CSRFDoubleSubmitCookieName)
	if doubleCookie == nil || doubleCookie.Value == "" {
		t.Fatalf("GET /login did not issue a %s cookie", auth.CSRFDoubleSubmitCookieName)
	}
	// 把会话 cookie 与双提交 cookie 一起带上：会话仍然有效（请求算“已登录”）。
	jar := append(append([]*http.Cookie{}, cookies...), doubleCookie)

	rec := postJSON(srv, "/api/v1/auth/forgot-password",
		map[string]string{"email": "admin@example.com"}, jar,
		map[string]string{auth.CSRFHeaderName: doubleCookie.Value})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a double-submit pair must not pass while signed in: %d %s", rec.Code, snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "csrf_failed") {
		t.Errorf("expected the stable csrf_failed code, got %s", snippet(rec.Body.String()))
	}
}

// TestPreSessionCSRFAnonymousPathUnchanged 是回归护栏：未登录时双提交仍然是唯一通道，
// 且「带会话 token 却没有会话」不成立——匿名请求只有带上 cookie 对才能通过。
func TestPreSessionCSRFAnonymousPathUnchanged(t *testing.T) {
	srv, _ := newAuthServer(t)

	// 匿名 + 有 cookie 对 → 放行（forgot-password 不依赖令牌有效性）。
	cookie, token := doubleSubmitFromShell(t, srv)
	ok := postJSON(srv, "/api/v1/auth/forgot-password",
		map[string]string{"email": "someone@example.com"},
		[]*http.Cookie{cookie}, map[string]string{auth.CSRFHeaderName: token})
	if ok.Code != http.StatusOK {
		t.Fatalf("anonymous double-submit pair should pass: %d %s", ok.Code, snippet(ok.Body.String()))
	}

	// 匿名 + 只给头（没有 cookie）→ 403。
	bad := postJSON(srv, "/api/v1/auth/forgot-password",
		map[string]string{"email": "someone@example.com"},
		nil, map[string]string{auth.CSRFHeaderName: token})
	if bad.Code != http.StatusForbidden {
		t.Fatalf("anonymous request without the cookie must be rejected: %d %s", bad.Code, snippet(bad.Body.String()))
	}
}

// TestPreSessionCSRFEmailLinkCompletesWhileSignedIn 是这次修复的端到端证据：**已登录**用户
// 点邮件里的重置链接（真实令牌）能真正把密码改掉，而修复前它在 CSRF 层就被 403 挡死。
func TestPreSessionCSRFEmailLinkCompletesWhileSignedIn(t *testing.T) {
	ts := newSecurityServer(t, true) // 夹具已用 owner 登录，ts.csrf 是会话绑定 token

	resetToken, _ := issueResetToken(t, ts)
	rec := postJSON(ts.srv, "/api/v1/auth/reset-password",
		map[string]string{"token": resetToken, "password": "N3wSup3rSecret!"},
		ts.cookies, map[string]string{auth.CSRFHeaderName: ts.csrf})
	if rec.Code != http.StatusOK {
		t.Fatalf("signed-in reset with a real token = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// 密码确实变了：用新密码走匿名登录应当成功。
	cookie, headers := preSessionPair(t, ts.srv, "/login")
	login := postJSON(ts.srv, "/api/v1/auth/login",
		map[string]string{"username": "owner", "password": "N3wSup3rSecret!"},
		[]*http.Cookie{cookie}, headers)
	if login.Code != http.StatusOK {
		t.Fatalf("login with the new password = %d, want 200 (body %s)", login.Code, snippet(login.Body.String()))
	}
}
