package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// 页面型 GET 的统一契约。
//
// 服务端已删除全部页面渲染（SSR 页面层整体移除），每个页面路径只剩两件事：先判定可达性
// （会话 / 角色 / 首启窗口），通过则返回应用壳，由客户端路由渲染页面。这些判定此前按页面逐页
// 重复同一形态；这里合成一张表，各页自己的业务断言仍留在各自的 *_test.go 里。

// assertShell 断言响应是 SPA 应用壳：200、text/html、revalidation/no-cache、带 ETag，
// 且含应用挂载点 <div id="app"></div>。
func assertShell(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Errorf("Cache-Control = %q, want containing no-cache", cc)
	}
	if rec.Header().Get("ETag") == "" {
		t.Errorf("ETag is missing")
	}
	if !strings.Contains(rec.Body.String(), `<div id="app"></div>`) {
		t.Errorf("response is not the SPA shell: %s", snippet(rec.Body.String()))
	}
}

// assertRedirectsToLogin 断言匿名请求被送去登录页，而不是拿到应用壳。
func assertRedirectsToLogin(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Errorf("anonymous %s = %d %q, want 303 /login", what, rec.Code, rec.Header().Get("Location"))
	}
	if strings.Contains(rec.Body.String(), `<div id="app"></div>`) {
		t.Errorf("anonymous %s returned the SPA shell", what)
	}
}

// TestSessionPageRoutesServeShell 覆盖「只要求已登录会话」的页面路径。
func TestSessionPageRoutesServeShell(t *testing.T) {
	for _, path := range []string{"/decks", "/review", "/settings", "/settings/security", "/settings/email", "/settings/keys"} {
		t.Run(path, func(t *testing.T) {
			srv, _, _, cookies, _ := newNotesServer(t)
			assertShell(t, getWithCookies(t, srv, path, cookies))
			assertRedirectsToLogin(t, getWithCookies(t, srv, path, nil), "GET "+path)
		})
	}
}

// TestPresetsRouteServesShell 覆盖 /presets：它需要预设页的装配（jobRunner 等）。
func TestPresetsRouteServesShell(t *testing.T) {
	srv, _, _, cookies, _, _ := newPresetsServer(t)
	assertShell(t, getWithCookies(t, srv, "/presets", cookies))
	assertRedirectsToLogin(t, getWithCookies(t, srv, "/presets", nil), "GET /presets")
}

// deckPageRouteCase 一行 = 一条卡组范围内页面路径的可达性断言。
type deckPageRouteCase struct {
	name string
	// path 由夹具的卡组 / 卡片对外 id 组成。
	path func(deckPublicID, notePublicID string) string
	// stranger 是「已登录但无权」的会话：none = 无任何授权，reader = 被授予 reader。
	stranger string
}

// TestDeckScopedPageRoutesEnforceRole 覆盖卡组范围内的页面路径：owner 拿到应用壳，匿名重定向
// 登录页，无权用户 403 且拿不到外壳——判权先于发壳执行。
func TestDeckScopedPageRoutesEnforceRole(t *testing.T) {
	cases := []deckPageRouteCase{
		{"note list", func(d, _ string) string { return "/decks/" + d + "/notes" }, "none"},
		{"note edit", func(d, n string) string { return "/decks/" + d + "/notes/" + n }, "reader"},
		{"note create", func(d, _ string) string { return "/decks/" + d + "/new-note" }, "reader"},
		{"deck settings", func(d, _ string) string { return "/decks/" + d + "/settings" }, "none"},
		{"deck sharing", func(d, _ string) string { return "/decks/" + d + "/sharing" }, "reader"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
			deck := seedReviewDeck(t, db, ownerID, "Page route deck")
			note := seedBasic(t, db, deck.ID, "Q", "A")
			path := tc.path(deck.PublicID, note.PublicID)

			assertShell(t, getWithCookies(t, srv, path, ownerCookies))
			assertRedirectsToLogin(t, get(t, srv, path, nil), "GET "+path)

			strangerID, strangerCookies, _ := createUserAndLogin(t, srv, db, "page-route-"+strings.ReplaceAll(tc.name, " ", "-"))
			if tc.stranger == "reader" {
				grantRole(t, srv, deck.ID, strangerID, store.RoleReader, ownerCookies, ownerCSRF)
			}
			denied := getWithCookies(t, srv, path, strangerCookies)
			if denied.Code != http.StatusForbidden {
				t.Fatalf("%s GET %s = %d, want 403 (body %s)", tc.stranger, path, denied.Code, snippet(denied.Body.String()))
			}
			if strings.Contains(denied.Body.String(), `<div id="app"></div>`) {
				t.Errorf("a denied user must not receive the SPA shell at %s", path)
			}
		})
	}
}

// TestHomeRouteBranches 覆盖 GET / 的两条互斥分支，书写顺序即优先级。
func TestHomeRouteBranches(t *testing.T) {
	t.Run("with an active admin", func(t *testing.T) {
		srv, _, _, cookies, _ := newNotesServer(t)
		assertShell(t, getWithCookies(t, srv, "/", cookies))

		// 首启窗口只看管理员数量，不做登录判定：匿名同样拿应用壳，否则会与 SPA 首页形成重定向循环。
		anon := getWithCookies(t, srv, "/", nil)
		assertShell(t, anon)
		if loc := anon.Header().Get("Location"); loc != "" {
			t.Errorf("anonymous GET / redirected to %q, want the SPA shell", loc)
		}
	})

	t.Run("first run redirects to setup", func(t *testing.T) {
		srv := newFreshServer(t, nil)
		rec := get(t, srv, "/", nil)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/setup" {
			t.Fatalf("GET / before any admin = %d %q, want 303 /setup", rec.Code, rec.Header().Get("Location"))
		}
		if strings.Contains(rec.Body.String(), `<div id="app"></div>`) {
			t.Error("the first-run redirect must not return the SPA shell")
		}
	})
}
