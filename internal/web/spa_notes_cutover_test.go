package web

import (
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件覆盖 GET /decks/:id/notes/:nid（卡片编辑）与 GET /decks/:id/new-note（新建卡片）两条
// 页面的 SPA 规范路径切流：返回应用壳（index.html），由客户端路由渲染页面。
//
// 两条路径的判权与迁移前一致：先要求已登录会话（匿名重定向登录页），再按 editor 角色
// 判定卡组可写性——reader 与陌生用户仍被拒（403），不因返回应用壳而放行。
// 写路径全部走同源 JSON 端点。
// 角色授予复用 media_access_test.go 里的 grantRole，直接写 store 的授权表。

// TestNoteEditRouteServesShell 断言 GET /decks/:id/notes/:nid 对 owner 返回 SPA 应用壳，
// 由客户端路由渲染编辑页，不再渲染 SSR 编辑表单。
func TestNoteEditRouteServesShell(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "SPA edit deck")
	note := seedBasic(t, db, deck.ID, "EditQ", "EditA")

	rec := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/notes/"+u64str(note.ID), cookies)
	assertShell(t, rec)
	if strings.Contains(rec.Body.String(), `name="field.front"`) {
		t.Errorf("GET edit still renders the SSR editor form: %s", snippet(rec.Body.String()))
	}
}

// TestNoteNewRouteServesShell 断言 GET /decks/:id/new-note 对 owner 返回 SPA 应用壳，
// 不再渲染 SSR 新建表单。
func TestNoteNewRouteServesShell(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "SPA create deck")

	rec := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/new-note", cookies)
	assertShell(t, rec)
	if strings.Contains(rec.Body.String(), "data-note-form") {
		t.Errorf("GET create still renders the SSR create form: %s", snippet(rec.Body.String()))
	}
}

// TestNoteEditRouteAllowsEditorAndDeniesReader 断言编辑壳沿用 SSR 编辑页的 editor 判权：
// 被授予 editor 的用户拿到应用壳，被授予 reader 的用户仍回 403。
func TestNoteEditRouteAllowsEditorAndDeniesReader(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Edit role deck")
	note := seedBasic(t, db, deck.ID, "RoleQ", "RoleA")
	editPath := "/decks/" + u64str(deck.ID) + "/notes/" + u64str(note.ID)

	editorID, editorCookies, _ := createUserAndLogin(t, srv, db, "notes-editor")
	grantRole(t, srv, deck.ID, editorID, store.RoleEditor, ownerCookies, ownerCSRF)
	assertShell(t, getWithCookies(t, srv, editPath, editorCookies))

	readerID, readerCookies, _ := createUserAndLogin(t, srv, db, "notes-reader")
	grantRole(t, srv, deck.ID, readerID, store.RoleReader, ownerCookies, ownerCSRF)
	if rec := getWithCookies(t, srv, editPath, readerCookies); rec.Code != http.StatusForbidden {
		t.Errorf("reader GET edit status = %d, want 403", rec.Code)
	}
}

// TestNoteNewRouteAllowsEditorAndDeniesReader 断言新建壳沿用 SSR 新建页的 editor 判权：
// editor 拿到应用壳，reader 仍回 403。
func TestNoteNewRouteAllowsEditorAndDeniesReader(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Create role deck")
	createPath := "/decks/" + u64str(deck.ID) + "/new-note"

	editorID, editorCookies, _ := createUserAndLogin(t, srv, db, "notes-create-editor")
	grantRole(t, srv, deck.ID, editorID, store.RoleEditor, ownerCookies, ownerCSRF)
	assertShell(t, getWithCookies(t, srv, createPath, editorCookies))

	readerID, readerCookies, _ := createUserAndLogin(t, srv, db, "notes-create-reader")
	grantRole(t, srv, deck.ID, readerID, store.RoleReader, ownerCookies, ownerCSRF)
	if rec := getWithCookies(t, srv, createPath, readerCookies); rec.Code != http.StatusForbidden {
		t.Errorf("reader GET create status = %d, want 403", rec.Code)
	}
}

// TestNotePageRoutesRedirectAnonymous 断言两条切壳路径都不把应用壳交给未登录访客：
// 匿名一律 303 重定向到登录页。
func TestNotePageRoutesRedirectAnonymous(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Anon deck")
	note := seedBasic(t, db, deck.ID, "AnonQ", "AnonA")

	for _, target := range []string{
		"/decks/" + u64str(deck.ID) + "/notes/" + u64str(note.ID),
		"/decks/" + u64str(deck.ID) + "/new-note",
	} {
		rec := get(t, srv, target, nil)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("anonymous GET %s status = %d, want 303", target, rec.Code)
			continue
		}
		if loc := rec.Header().Get("Location"); loc != "/login" {
			t.Errorf("anonymous GET %s Location = %q, want /login", target, loc)
		}
	}
}
