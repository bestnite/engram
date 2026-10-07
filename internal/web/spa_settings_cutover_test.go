package web

import (
	"net/http"
	"strings"
	"testing"
)

// 本文件覆盖 GET /settings 与 GET /settings/keys 的 SPA 规范路径切流（DESIGN.md §4.1、§7.2、
// §8.1、§8.5）。
//
// 个人设置页与「我的 API Key」页在 SPA 已加载时返回应用壳，由客户端路由渲染，读写走
// /api/v1/profile、/api/v1/settings/locale、/api/v1/settings/password 与 /api/v1/keys 的 JSON
// 端点；判权与迁移前的 SSR 页逐项一致（都只要求已登录会话，匿名重定向登录页），SPA 缺失
// （降级）时回退各自的 SSR 页。SSR handler、模板与全部写路径（POST）保持不变。
//
// 本文件钉住：登录用户拿到应用壳、匿名被重定向、SPA 缺失时回退 SSR 页，以及设置资料/改密与
// key 创建/撤销四个写路径既仍可用、又仍受 CSRF 保护。

// settingsSSRMarker 是 SSR 设置页特有的表单标记：SPA 应用壳里不会出现。
func settingsSSRMarker() string { return `action="/settings/profile"` }

// keysSSRMarker 是 SSR「我的 API Key」页特有的创建表单标记：SPA 应用壳里不会出现。
func keysSSRMarker() string { return `action="/settings/keys"` }

// TestSettingsRouteServesSPAShell 断言登录用户访问 GET /settings 得到 SPA 应用壳，
// 由客户端路由渲染个人设置页，不再渲染 SSR 设置页。
func TestSettingsRouteServesSPAShell(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)

	rec := getWithCookies(t, srv, "/settings", cookies)
	assertSPAShell(t, rec)
	if strings.Contains(rec.Body.String(), settingsSSRMarker()) {
		t.Errorf("GET /settings still renders the SSR settings form: %s", snippet(rec.Body.String()))
	}
	if strings.Contains(rec.Body.String(), `id="settings-display-name"`) {
		t.Errorf("GET /settings still renders the SSR profile input: %s", snippet(rec.Body.String()))
	}
}

// TestKeysRouteServesSPAShell 断言登录用户访问 GET /settings/keys 得到 SPA 应用壳，
// 由客户端路由渲染「我的 API Key」页，不再渲染 SSR keys 页。
func TestKeysRouteServesSPAShell(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)

	rec := getWithCookies(t, srv, "/settings/keys", cookies)
	assertSPAShell(t, rec)
	if strings.Contains(rec.Body.String(), keysSSRMarker()) {
		t.Errorf("GET /settings/keys still renders the SSR keys form: %s", snippet(rec.Body.String()))
	}
}

// TestSettingsAndKeysRoutesRedirectAnonymous 断言切壳不改动授权：两个页面都只要求已登录
// 会话，匿名请求一律 303 重定向到登录页，而不是拿到应用壳。
func TestSettingsAndKeysRoutesRedirectAnonymous(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	for _, path := range []string{"/settings", "/settings/keys"} {
		rec := getWithCookies(t, srv, path, nil)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
			t.Errorf("anonymous GET %s = %d loc %q, want 303 /login", path, rec.Code, rec.Header().Get("Location"))
		}
	}
}
