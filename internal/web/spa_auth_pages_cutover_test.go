package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
)

// 本文件是三条登录前入口（GET /login、GET /register、GET /setup）切流到 SPA 的验收。
//
// 契约：SPA 已加载时返回应用壳并像 SSR 页面一样先下发会话前双提交 cookie；SSR 未装配
// （降级构建）时回退各自的 SSR 页面；POST 表单处理器始终留在 SSR，因此无脚本客户端仍可
// 登录/注册/引导。可达性判定（引导窗口）不因页面迁移而变化。

// assertSPAAuthShell 断言响应是应用壳、带双提交 cookie，且不发会话 cookie。
func assertSPAAuthShell(t *testing.T, rec *httptest.ResponseRecorder, srv *Server, what string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status = %d, want 200 (body %s)", what, rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="app"`) {
		t.Errorf("%s did not serve the SPA shell: %s", what, snippet(body))
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

// TestSPALoginRegisterShellsCutOver 覆盖 GET /login 与 GET /register 的切流与降级回退。
func TestSPALoginRegisterShellsCutOver(t *testing.T) {
	srv, _ := newAuthServer(t)

	for _, path := range []string{"/login", "/register"} {
		assertSPAAuthShell(t, get(t, srv, path, nil), srv, "GET "+path)
	}
	// 迁移期别名与规范路径共用同一处理器。
	for _, path := range []string{"/spa/login", "/spa/register"} {
		assertSPAAuthShell(t, get(t, srv, path, nil), srv, "GET "+path)
	}

	// SPA 未装配：回退 SSR 表单页，表单里带双提交镜像 token。
	srv.spa = nil
	marker := `name="` + auth.CSRFFieldName + `"`
	for _, path := range []string{"/login", "/register"} {
		rec := get(t, srv, path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("SSR fallback GET %s = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), marker) {
			t.Errorf("SSR fallback GET %s is missing %s: %s", path, marker, snippet(rec.Body.String()))
		}
	}
}

// TestSPASetupShellCutOver 覆盖 GET /setup 的切流、降级回退与一次性管理员门。
func TestSPASetupShellCutOver(t *testing.T) {
	srv, _ := newAuthServer(t)

	// 尚无管理员：两条路径都可达并返回应用壳。
	assertSPAAuthShell(t, get(t, srv, "/setup", nil), srv, "GET /setup (first run)")
	assertSPAAuthShell(t, get(t, srv, "/spa/setup", nil), srv, "GET /spa/setup (first run)")

	// 降级回退：SSR 引导页。
	srv.spa = nil
	fallback := get(t, srv, "/setup", nil)
	if fallback.Code != http.StatusOK {
		t.Fatalf("SSR fallback GET /setup = %d, want 200", fallback.Code)
	}
	if !strings.Contains(fallback.Body.String(), `name="`+auth.CSRFFieldName+`"`) {
		t.Errorf("SSR fallback /setup is missing the CSRF field: %s", snippet(fallback.Body.String()))
	}

	// 建出首个管理员（走 SSR 表单，POST 始终留在 SSR）。
	rec := postForm(t, srv, "/setup", url.Values{
		"username": {"admin"},
		"email":    {"admin@example.com"},
		"password": {"Sup3rSecret!"},
	}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /setup = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// 引导窗口关闭：两条路径都必须 404，且不得回退 SSR 引导页。
	for _, path := range []string{"/setup", "/spa/setup"} {
		after := get(t, srv, path, nil)
		if after.Code != http.StatusNotFound {
			t.Errorf("GET %s after the first admin = %d, want 404 (body %s)", path, after.Code, snippet(after.Body.String()))
		}
		if strings.Contains(after.Body.String(), `name="`+auth.CSRFFieldName+`"`) {
			t.Errorf("GET %s leaked the SSR setup form after the window closed", path)
		}
	}
}
