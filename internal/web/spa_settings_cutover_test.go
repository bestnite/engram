package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
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

// TestSettingsRouteFallsBackToSSR 断言 SPA 缺失（降级）时 GET /settings 回退 SSR 设置页：
// 模板与 handler 全部保留，不是应用壳。
func TestSettingsRouteFallsBackToSSR(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	srv.spa = nil

	rec := getWithCookies(t, srv, "/settings", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /settings fallback = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if strings.Contains(rec.Body.String(), `<div id="app"></div>`) {
		t.Errorf("fallback returned the SPA shell; the SSR settings page must be preserved")
	}
	if !strings.Contains(rec.Body.String(), settingsSSRMarker()) {
		t.Errorf("fallback is missing the SSR settings form: %s", snippet(rec.Body.String()))
	}
}

// TestKeysRouteFallsBackToSSR 断言 SPA 缺失（降级）时 GET /settings/keys 回退 SSR keys 页：
// 模板与 handler 全部保留，不是应用壳。
func TestKeysRouteFallsBackToSSR(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	srv.spa = nil

	rec := getWithCookies(t, srv, "/settings/keys", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /settings/keys fallback = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if strings.Contains(rec.Body.String(), `<div id="app"></div>`) {
		t.Errorf("fallback returned the SPA shell; the SSR keys page must be preserved")
	}
	if !strings.Contains(rec.Body.String(), keysSSRMarker()) {
		t.Errorf("fallback is missing the SSR keys form: %s", snippet(rec.Body.String()))
	}
}

// TestSettingsMutationsKeepCSRF 断言切壳没有移除个人设置的写路径，也没有放松 CSRF：
// 资料表单与改密缺 token 都 403，带正确 token 才通过。
func TestSettingsMutationsKeepCSRF(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)

	profile := url.Values{
		"display_name":    {"Owner"},
		"locale":          {"zh-CN"},
		"timezone":        {"UTC"},
		"day_cutoff_hour": {"4"},
	}
	without := url.Values{}
	for k, v := range profile {
		without[k] = v
	}
	if rec := postForm(t, srv, "/settings/profile", without, cookies); rec.Code != http.StatusForbidden {
		t.Errorf("POST /settings/profile without CSRF = %d, want 403", rec.Code)
	}
	with := url.Values{"csrf_token": {csrf}}
	for k, v := range profile {
		with[k] = v
	}
	if rec := postForm(t, srv, "/settings/profile", with, cookies); rec.Code != http.StatusSeeOther {
		t.Errorf("POST /settings/profile with CSRF = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	if rec := postForm(t, srv, "/settings/password", url.Values{
		"old_password": {"Sup3rSecret!"}, "new_password": {"N3wStrongPassword!"},
	}, cookies); rec.Code != http.StatusForbidden {
		t.Errorf("POST /settings/password without CSRF = %d, want 403", rec.Code)
	}
	if rec := postForm(t, srv, "/settings/password", url.Values{
		"csrf_token": {csrf}, "old_password": {"Sup3rSecret!"}, "new_password": {"N3wStrongPassword!"},
	}, cookies); rec.Code != http.StatusSeeOther {
		t.Errorf("POST /settings/password with CSRF = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
}

// TestKeysMutationsKeepCSRF 断言切壳没有移除 key 的创建与撤销写路径，也没有放松 CSRF：
// 两者缺 token 都 403，带正确 token 才通过（创建 200 并显示一次明文，撤销 303 回列表）。
func TestKeysMutationsKeepCSRF(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)

	if rec := postForm(t, srv, "/settings/keys", url.Values{
		"name": {"cutover key"}, "scopes": {store.ScopeRead},
	}, cookies); rec.Code != http.StatusForbidden {
		t.Errorf("POST /settings/keys without CSRF = %d, want 403", rec.Code)
	}
	create := postForm(t, srv, "/settings/keys", url.Values{
		"csrf_token": {csrf}, "name": {"cutover key"}, "scopes": {store.ScopeRead},
	}, cookies)
	if create.Code != http.StatusOK {
		t.Fatalf("POST /settings/keys with CSRF = %d, want 200 (body %s)", create.Code, snippet(create.Body.String()))
	}
	keys, err := store.NewAPIKeyStore(db).ListByUser(context.Background(), ownerID)
	if err != nil || len(keys) != 1 {
		t.Fatalf("after create: keys = %d, err = %v, want 1", len(keys), err)
	}
	revokePath := "/settings/keys/" + strconv.FormatUint(keys[0].ID, 10) + "/revoke"

	if rec := postForm(t, srv, revokePath, url.Values{}, cookies); rec.Code != http.StatusForbidden {
		t.Errorf("POST revoke without CSRF = %d, want 403", rec.Code)
	}
	if rec := postForm(t, srv, revokePath, url.Values{"csrf_token": {csrf}}, cookies); rec.Code != http.StatusSeeOther {
		t.Errorf("POST revoke with CSRF = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var stored store.APIKey
	if err := db.First(&stored, "id = ?", keys[0].ID).Error; err != nil {
		t.Fatalf("reload key: %v", err)
	}
	if stored.RevokedAt == nil {
		t.Errorf("key was not revoked after a CSRF-protected POST")
	}
}
