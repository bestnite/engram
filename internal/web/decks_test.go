package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"example.com/engram/internal/store"
)

// TestDeckCreateAppearsInList 是 M2-11 的主验收：登录用户在 /decks 建卡组后能在列表中看到它。
func TestDeckCreateAppearsInList(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)

	// 无预设时，创建卡组应自动补一个默认预设，保证新用户也能建组。
	rec := postForm(t, srv, "/decks", url.Values{
		"csrf_token":  {csrf},
		"name":        {"My first deck"},
		"description": {"created through the browser"},
	}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /decks status = %d, want 303 (body %s)", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/decks" {
		t.Fatalf("POST /decks Location = %q, want /decks", loc)
	}

	var deck store.Deck
	if err := db.Where("owner_user_id = ? AND name = ?", ownerID, "My first deck").First(&deck).Error; err != nil {
		t.Fatalf("deck was not persisted: %v", err)
	}
	if deck.PresetID == 0 {
		t.Errorf("created deck has no preset")
	}

	page := getWithCookies(t, srv, "/decks", cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /decks status = %d, want 200 (body %s)", page.Code, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), "My first deck") {
		t.Errorf("GET /decks body does not list the created deck: %s", page.Body.String())
	}

	// 写操作必须落一行审计（ROADMAP.md M2-11）。
	n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckCreate)
	if err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if n != 1 {
		t.Errorf("audit rows for %s = %d, want 1", store.ActionDeckCreate, n)
	}
}

// TestDeckListRedirectsAnonymousToLogin 覆盖匿名访问被重定向到登录页的验收点。
func TestDeckListRedirectsAnonymousToLogin(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/decks", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /decks (anonymous) status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("GET /decks (anonymous) Location = %q, want /login", loc)
	}
}

// TestDeckCreateRequiresCSRF 是必测的负例：缺少 CSRF token 的写请求被拒，且不落库。
func TestDeckCreateRequiresCSRF(t *testing.T) {
	srv, db, _, cookies, _ := newNotesServer(t)
	rec := postForm(t, srv, "/decks", url.Values{"name": {"no csrf"}}, cookies)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /decks without CSRF status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	var n int64
	if err := db.Model(&store.Deck{}).Where("name = ?", "no csrf").Count(&n).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if n != 0 {
		t.Errorf("deck created despite missing CSRF: count = %d", n)
	}
}

// TestDeckCreateRejectsEmptyName 覆盖表单校验：空名称回显 400，且不落库。
func TestDeckCreateRejectsEmptyName(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	rec := postForm(t, srv, "/decks", url.Values{"csrf_token": {csrf}, "name": {"   "}}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /decks with empty name status = %d, want 400", rec.Code)
	}
	var n int64
	if err := db.Model(&store.Deck{}).Count(&n).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if n != 0 {
		t.Errorf("deck count = %d, want 0", n)
	}
}
