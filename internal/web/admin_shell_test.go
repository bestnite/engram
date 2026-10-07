package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// newAdminServer 复用 newNotesServer：它创建的 owner 就是管理员，同时提供 admin 会话与 CSRF。
func newAdminServer(t *testing.T) (srv *Server, adminCookies []*http.Cookie, adminCSRF string) {
	t.Helper()
	srv, _, _, cookies, csrf := newNotesServer(t)
	return srv, cookies, csrf
}

// TestAdminRoutesDenyNonAdmin 是 M6-1 的核心验收：建一个普通用户，逐条访问
// 已注册的每一个 /admin/* 路由，全部必须 403（写路由在守卫处即被拦下，不看 CSRF）。
func TestAdminRoutesDenyNonAdmin(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	_, cookies, _ := createUserAndLogin(t, srv, db, "plainuser")

	routes := adminRoutes()
	if len(routes) == 0 {
		t.Fatal("adminRoutes() is empty; the guard test would be vacuous")
	}
	for _, r := range routes {
		var rec *httptest.ResponseRecorder
		if r.Write {
			rec = postForm(t, srv, r.Path, url.Values{}, cookies)
		} else {
			rec = getWithCookies(t, srv, r.Path, cookies)
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as non-admin = %d, want 403 (body %s)", r.Method, r.Path, rec.Code, snippet(rec.Body.String()))
		}
	}

	// 匿名访问管理面板不暴露 403 语义，而是回到登录页。
	anon := getWithCookies(t, srv, "/admin", nil)
	if anon.Code != http.StatusSeeOther {
		t.Errorf("anonymous GET /admin = %d, want 303 redirect to login", anon.Code)
	}
	// 越权尝试写审计。
	n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionPermissionDenied)
	if err != nil {
		t.Fatalf("count permission.denied audits: %v", err)
	}
	if n == 0 {
		t.Error("no permission.denied audit rows written for admin route attempts")
	}
}

// TestAdminShellShowsFullNavigation 断言导航立住了所有子页的入口。
// M6-9 / M8-4 落地后，导航里已没有任何置灰子页：每个入口都必须是可用链接。
func TestAdminShellShowsFullNavigation(t *testing.T) {
	srv, cookies, _ := newAdminServer(t)
	// GET /admin 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 外壳的导航全貌断言。
	srv.spa = nil
	rec := getWithCookies(t, srv, "/admin", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin as admin = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()
	for _, label := range []string{"用户管理", "注册与邀请", "身份与 OIDC", "系统设置", "作业", "审计", "健康", "API Key", "语言包"} {
		if !strings.Contains(body, label) {
			t.Errorf("admin shell navigation is missing %q", label)
		}
	}
	// 每个子页都已实现：导航里不应再出现「未实现」标记。
	if strings.Contains(body, "未实现") {
		t.Errorf("admin navigation still marks a subpage as pending; body = %s", snippet(body))
	}
}
