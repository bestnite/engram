package web

import (
	"context"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// newAdminServer 复用 newNotesServer：它创建的 owner 就是管理员，同时提供 admin 会话与 CSRF。
func newAdminServer(t *testing.T) (srv *Server, adminCookies []*http.Cookie, adminCSRF string) {
	t.Helper()
	srv, _, _, cookies, csrf := newNotesServer(t)
	return srv, cookies, csrf
}

// TestAdminRoutesDenyNonAdmin 是核心验收：建一个普通用户，逐条访问
// 已注册的每一个 /admin/* 页面路由，全部必须 403（守卫先于外壳）。
func TestAdminRoutesDenyNonAdmin(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	_, cookies, _ := createUserAndLogin(t, srv, db, "plainuser")

	routes := adminRoutes()
	if len(routes) == 0 {
		t.Fatal("adminRoutes() is empty; the guard test would be vacuous")
	}
	for _, r := range routes {
		rec := getWithCookies(t, srv, r.Path, cookies)
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
