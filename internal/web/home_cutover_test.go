package web

import (
	"net/http"
	"strings"
	"testing"
)

// 本文件覆盖 GET / 的 SPA 规范路径切流（首页「今日」）。
//
// homeRoute 的两条分支互斥，顺序即优先级：首启窗口（无活跃管理员）重定向 /setup 最高；
// 否则返回应用壳，由客户端路由渲染首页，数据走 JSON 端点。会话与鉴权判定先于切壳执行。
//
// 本文件只钉住切流本身与首启窗口的优先级；首页数据由 JSON 端点与前端测试覆盖。

// TestHomeRouteServesShellForSignedInUser 断言引导已完成时已登录用户访问 GET / 得到 SPA
// 应用壳，由客户端路由渲染首页。
func TestHomeRouteServesShellForSignedInUser(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)

	rec := getWithCookies(t, srv, "/", cookies)
	assertShell(t, rec)
}

// TestHomeRouteServesShellForAnonymousWithActiveAdmin 断言「管理员已存在但访客未登录」时
// GET / 仍返回 SPA 应用壳：首启窗口只看管理员数量，不做登录判定，匿名访客同样直接拿到应用壳。
// 这条同时钉住「不会误把匿名访客送回 /setup」，否则会与 SPA 首页形成重定向循环。
func TestHomeRouteServesShellForAnonymousWithActiveAdmin(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)

	rec := getWithCookies(t, srv, "/", nil)
	assertShell(t, rec)
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("anonymous GET / redirected to %q, want the SPA shell (no setup redirect once an admin exists)", loc)
	}
}

// TestHomeRouteRedirectsToSetupBeforeSPA 断言首启窗口优先级最高：还没有活跃管理员时，即使 SPA
// 已加载，GET / 仍 303 到 /setup，且响应体不是应用壳。这条覆盖「无管理员 → /setup 不变」。
func TestHomeRouteRedirectsToSetupBeforeShell(t *testing.T) {
	srv := newFreshServer(t, nil)

	rec := get(t, srv, "/", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET / before any admin = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if loc := rec.Header().Get("Location"); loc != "/setup" {
		t.Errorf("Location = %q, want /setup", loc)
	}
	if strings.Contains(rec.Body.String(), `<div id="app"></div>`) {
		t.Errorf("first-run redirect returned the SPA shell; /setup must win")
	}
}
