package web

import (
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
)

// 本文件是（会话前双提交 CSRF）的验收测试。
//
// 移除 SSR 会话前表单后，会话前的写请求全部是同源 JSON 端点（登录、注册、引导、找回密码、
// 重置密码、登录第二步）。它们仍由 auth.PreSessionCSRFMiddleware 保护：请求必须同时携带
// csrf_double cookie 与镜像 token（X-CSRF-Token 头或 csrf_token 表单字段）。

// doubleSubmitFromShell 走一次 GET /login 应用壳，返回下发的 csrf_double cookie 与镜像 token。
// 双提交语义下二者同值：token 也等于 GET /api/v1/auth/session 返回的 csrf_token。
func doubleSubmitFromShell(t *testing.T, srv *Server) (*http.Cookie, string) {
	t.Helper()
	rec := get(t, srv, "/login", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login status = %d, want 200", rec.Code)
	}
	cookie := findCookie(rec, auth.CSRFDoubleSubmitCookieName)
	if cookie == nil || cookie.Value == "" {
		t.Fatalf("GET /login did not issue a %s cookie (body %s)", auth.CSRFDoubleSubmitCookieName, snippet(rec.Body.String()))
	}
	return cookie, cookie.Value
}

// TestPreSessionCSRFNormalBrowserFlow 是正向验收：SPA 从应用壳拿到 cookie 与镜像 token，
// 再带着它们提交 JSON 写请求，引导与登录都能走通。
func TestPreSessionCSRFNormalBrowserFlow(t *testing.T) {
	srv, _ := newAuthServer(t)

	cookie, token := doubleSubmitFromShell(t, srv)
	setup := postJSON(srv, "/api/v1/auth/setup", map[string]string{
		"username": "root", "email": "root@example.com", "password": "Sup3rSecret!",
	}, []*http.Cookie{cookie}, map[string]string{auth.CSRFHeaderName: token})
	if setup.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/setup with a valid double-submit pair = %d, want 200 (body %s)", setup.Code, snippet(setup.Body.String()))
	}

	loginCookie, loginToken := doubleSubmitFromShell(t, srv)
	login := postJSON(srv, "/api/v1/auth/login", map[string]string{
		"username": "root", "password": "Sup3rSecret!",
	}, []*http.Cookie{loginCookie}, map[string]string{auth.CSRFHeaderName: loginToken})
	if login.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/login with a valid double-submit pair = %d, want 200 (body %s)", login.Code, snippet(login.Body.String()))
	}
}

// TestPreSessionCSRFRejections 是反面验收：缺少镜像 cookie 或值不匹配的会话前 JSON 写请求一律 403。
func TestPreSessionCSRFRejections(t *testing.T) {
	body := map[string]string{
		"username": "intruder", "email": "intruder@example.com", "password": "Sup3rSecret!",
	}
	cases := []struct {
		name   string
		path   string
		cookie string
		token  string
	}{
		{name: "no cookie and no token", path: "/api/v1/auth/login"},
		{name: "token without cookie", path: "/api/v1/auth/login", token: testDoubleSubmitToken},
		{name: "cookie without mirrored token", path: "/api/v1/auth/register", cookie: "a-cookie-value-0123456789ab"},
		{name: "cookie and token mismatch", path: "/api/v1/auth/register",
			cookie: "cookie-value-0123456789abc", token: "different-token-0123456789"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newAuthServer(t)
			var cookies []*http.Cookie
			if tc.cookie != "" {
				cookies = append(cookies, &http.Cookie{Name: auth.CSRFDoubleSubmitCookieName, Value: tc.cookie})
			}
			headers := map[string]string{}
			if tc.token != "" {
				headers[auth.CSRFHeaderName] = tc.token
			}
			rec := postJSON(srv, tc.path, body, cookies, headers)
			if rec.Code != http.StatusForbidden || apiErrorCode(t, rec) != "csrf_failed" {
				t.Fatalf("%s: POST %s = %d %s, want 403 csrf_failed", tc.name, tc.path, rec.Code, apiErrorCode(t, rec))
			}
		})
	}
}

// TestSessionCSRFTokenDoesNotAuthorizePreSessionForms 断言两条路径互相独立：
// 会话绑定的 token 不是双提交 cookie（不能放行会话前端点），双提交 cookie 也不能放行会话端点。
func TestSessionCSRFTokenDoesNotAuthorizePreSessionForms(t *testing.T) {
	srv, db := newAuthServer(t)
	seedAdminUser(t, srv)

	// 建立会话，拿到会话绑定的 CSRF token（服务端状态）。
	sessionCookies, sessionCSRF := loginJSON(t, srv, db, "admin", "Sup3rSecret!")

	// 会话 token 缺少 csrf_double cookie：会话前端点必须拒绝。
	noDouble := postJSON(srv, "/api/v1/auth/register", map[string]string{
		"username": "intruder", "email": "intruder@example.com", "password": "Sup3rSecret!",
	}, sessionCookies, map[string]string{auth.CSRFHeaderName: sessionCSRF})
	if noDouble.Code != http.StatusForbidden {
		t.Fatalf("pre-session endpoint with only a session token = %d, want 403", noDouble.Code)
	}

	// 双提交 cookie 不能当作会话绑定的 CSRF：带会话但没有会话 token，登出必须 403。
	doubleCookie, _ := doubleSubmitFromShell(t, srv)
	logout := postJSON(srv, "/api/v1/auth/logout", nil,
		append(sessionCookies, doubleCookie), nil)
	if logout.Code != http.StatusForbidden {
		t.Fatalf("POST /api/v1/auth/logout with only a double-submit cookie = %d, want 403", logout.Code)
	}
}
