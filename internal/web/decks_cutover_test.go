package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 本文件覆盖 GET /decks 与 GET /decks/:id/notes 的 SPA 规范路径切流：
// 两条路径都返回应用壳（index.html），由客户端路由渲染页面；鉴权判定（会话 + 角色）先于切壳执行。
// 卡片编辑/新建两条 GET 路径的切流断言在 notes_cutover_test.go。

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

// TestDeckListRouteServesShell 断言 GET /decks 返回 SPA 应用壳，由客户端路由渲染卡组列表，
// 页面正文不含任何服务端渲染的列表数据（应用壳是静态入口）。
func TestDeckListRouteServesShell(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	seedDeck(t, db, ownerID, "List deck")

	rec := getWithCookies(t, srv, "/decks", cookies)
	assertShell(t, rec)
}

// TestDeckListRouteRedirectsAnonymous 断言切壳不改动授权：匿名访问 GET /decks 仍重定向登录页，
// 应用壳不会泄漏给未登录访客。
func TestDeckListRouteRedirectsAnonymous(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/decks", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("anonymous GET /decks status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("anonymous GET /decks Location = %q, want /login", loc)
	}
}

// TestNoteListRouteServesShell 断言 GET /decks/:id/notes 对可读卡组返回 SPA 应用壳，
// 由客户端路由渲染卡片列表。
func TestNoteListRouteServesShell(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Notes deck")
	seedBasic(t, db, deck.ID, "Q1", "A1")

	rec := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/notes", cookies)
	assertShell(t, rec)
}

// TestNoteListRouteEnforcesDeckRole 断言授权判定不被发壳绕过：匿名重定向登录页，
// 无权读的卡组仍 403。
func TestNoteListRouteEnforcesDeckRole(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Private notes deck")

	if code := get(t, srv, "/decks/"+u64str(deck.ID)+"/notes", nil).Code; code != http.StatusSeeOther {
		t.Errorf("anonymous GET notes status = %d, want 303 redirect to login", code)
	}

	_, u2Cookies, _ := createUserAndLogin(t, srv, db, "notes_stranger")
	if rec := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/notes", u2Cookies); rec.Code != http.StatusForbidden {
		t.Errorf("foreign GET notes status = %d, want 403", rec.Code)
	}
}
