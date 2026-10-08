package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
)

// 本文件是三条登录前入口（GET /login、GET /register、GET /setup）的应用壳验收。
//
// 契约：页面只返回应用壳，并像从前的 SSR 页面一样先下发会话前双提交 cookie；不再有任何 SSR
// 回退。可达性判定（引导窗口）不因页面迁移而变化，写操作全部走 /api/v1/auth/* 的 JSON 端点。

// assertAuthShell 断言响应是应用壳、带双提交 cookie，且不发会话 cookie。
func assertAuthShell(t *testing.T, rec *httptest.ResponseRecorder, srv *Server, what string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status = %d, want 200 (body %s)", what, rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="app"`) {
		t.Errorf("%s did not serve the SPA shell: %s", what, snippet(body))
	}
	if strings.Contains(body, `name="password"`) || strings.Contains(body, `name="csrf_token"`) {
		t.Errorf("%s must not render an SSR form: %s", what, snippet(body))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("%s Content-Type = %q, want text/html", what, ct)
	}
	if findCookie(rec, auth.CSRFDoubleSubmitCookieName) == nil {
		t.Errorf("%s did not set the %s cookie", what, auth.CSRFDoubleSubmitCookieName)
	}
	if findCookie(rec, srv.sessions.CookieName()) != nil {
		t.Errorf("%s must not issue a session cookie", what)
	}
}

// TestLoginRegisterShellsCutOver 覆盖 GET /login 与 GET /register 的应用壳。
func TestLoginRegisterShellsCutOver(t *testing.T) {
	srv, _ := newAuthServer(t)

	for _, path := range []string{"/login", "/register"} {
		assertAuthShell(t, get(t, srv, path, nil), srv, "GET "+path)
	}
}

// TestSetupShellCutOver 覆盖 GET /setup 的应用壳与一次性管理员门。
func TestSetupShellCutOver(t *testing.T) {
	srv, _ := newAuthServer(t)

	// 尚无管理员：可达并返回应用壳。
	assertAuthShell(t, get(t, srv, "/setup", nil), srv, "GET /setup (first run)")

	// 建出首个管理员（走 JSON 引导端点）。
	if setup := setupFirstAdmin(t, srv, "admin", "admin@example.com", "Sup3rSecret!"); setup.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/setup = %d, want 200 (body %s)", setup.Code, snippet(setup.Body.String()))
	}

	// 引导窗口关闭：必须 404，且不得泄漏 SSR 引导表单。
	for _, path := range []string{"/setup"} {
		after := get(t, srv, path, nil)
		if after.Code != http.StatusNotFound {
			t.Errorf("GET %s after the first admin = %d, want 404 (body %s)", path, after.Code, snippet(after.Body.String()))
		}
		if strings.Contains(after.Body.String(), `name="`+auth.CSRFFieldName+`"`) {
			t.Errorf("GET %s leaked the SSR setup form after the window closed", path)
		}
	}
}
