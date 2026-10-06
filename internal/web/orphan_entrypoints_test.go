package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件覆盖「孤立页面补链」任务：把已存在但此前没有入口的页面挂到上下文合适的入口上。
//   - /import：卡组列表头部的导入按钮（不塞进全局导航的第六项）；
//   - /decks/:id/sharing：仅 owner 行给出共享入口，被共享方（非 owner）不出现；
//   - /settings/email：个人设置页的改邮箱卡片，仅安全邮件栈启用时渲染。
//
// 测试全部走真实路由（httptest + 已登录会话），复用既有的 newNotesServer /
// newSecurityServer / seedDeck / createUserAndLogin 助手，不 mock 数据库。

// TestDeckListImportAndOwnerSharingEntryPoints 断言卡组列表页把 /import 与 owner 行的
// /decks/:id/sharing 暴露为可点入口，同时非 owner 的共享卡组行不出现共享入口。
func TestDeckListImportAndOwnerSharingEntryPoints(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	// GET /decks 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 回退列表页（DESIGN.md §8.5）。
	srv.spa = nil
	deck := seedDeck(t, db, ownerID, "Owned deck")
	sharingHref := `href="/decks/` + u64str(deck.ID) + `/sharing"`

	// owner 的列表：导入按钮 + 自己卡组的共享入口都在。
	ownerPage := getWithCookies(t, srv, "/decks", ownerCookies)
	if ownerPage.Code != http.StatusOK {
		t.Fatalf("GET /decks (owner) status = %d, want 200 (body %s)", ownerPage.Code, snippet(ownerPage.Body.String()))
	}
	ownerBody := ownerPage.Body.String()
	if !strings.Contains(ownerBody, `href="/import"`) {
		t.Errorf("owner /decks does not link to /import: %s", snippet(ownerBody))
	}
	if !strings.Contains(ownerBody, sharingHref) {
		t.Errorf("owner /decks does not link to the deck's /sharing page (%s): %s", sharingHref, snippet(ownerBody))
	}

	// 把 owner 的卡组共享给第二个用户；他应能在自己的列表里看到该卡组，但不该看到共享管理入口。
	user2ID, user2Cookies, _ := createUserAndLogin(t, srv, db, "reader2")
	if rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/sharing/grant", url.Values{
		"csrf_token": {ownerCSRF}, "username": {"reader2"}, "role": {store.RoleReader},
	}, ownerCookies); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST grant status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if user2ID == ownerID {
		t.Fatalf("test setup error: reader2 id equals owner id")
	}

	sharedPage := getWithCookies(t, srv, "/decks", user2Cookies)
	if sharedPage.Code != http.StatusOK {
		t.Fatalf("GET /decks (shared user) status = %d, want 200 (body %s)", sharedPage.Code, snippet(sharedPage.Body.String()))
	}
	sharedBody := sharedPage.Body.String()
	if !strings.Contains(sharedBody, "Owned deck") {
		t.Errorf("shared user's /decks does not list the granted deck: %s", snippet(sharedBody))
	}
	if !strings.Contains(sharedBody, `href="/import"`) {
		t.Errorf("shared user's /decks does not link to /import: %s", snippet(sharedBody))
	}
	if strings.Contains(sharedBody, sharingHref) {
		t.Errorf("shared user's /decks advertises the owner-only sharing entry (%s): %s", sharingHref, snippet(sharedBody))
	}
	t.Logf("owner sees %s; shared user sees the deck without that href", sharingHref)
}

// TestDeckListImportHiddenFromAnonymous 断言 /decks 仍是被保护路由：匿名访问被重定向到
// 登录页，因此 /import 入口不会泄漏给未登录访客。
func TestDeckListImportHiddenFromAnonymous(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/decks", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("anonymous GET /decks status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("anonymous GET /decks Location = %q, want /login", loc)
	}
	if strings.Contains(rec.Body.String(), `href="/import"`) {
		t.Errorf("anonymous /decks response leaked the /import entry: %s", snippet(rec.Body.String()))
	}
}

// TestSettingsEmailChangeEntryPointConditionalOnMailStack 断言改邮箱入口只在安全邮件栈
// 启用（SMTP 已配置且 outbox 已装配）时出现在 /settings 上，未启用时整张卡片不渲染。
func TestSettingsEmailChangeEntryPointConditionalOnMailStack(t *testing.T) {
	// 未装配邮件：设置页不应出现 /settings/email 入口。
	plainSrv, _, _, plainCookies, _ := newNotesServer(t)
	plain := getWithCookies(t, plainSrv, "/settings", plainCookies)
	if plain.Code != http.StatusOK {
		t.Fatalf("GET /settings (no mail) status = %d, want 200 (body %s)", plain.Code, snippet(plain.Body.String()))
	}
	if strings.Contains(plain.Body.String(), `href="/settings/email"`) {
		t.Errorf("settings page links to /settings/email although the mail stack is disabled: %s", snippet(plain.Body.String()))
	}

	// 未装配邮件的安全测试服务：同样不应出现入口（第二条独立路径）。
	noMail := newSecurityServer(t, false)
	noMailPage := getWithCookies(t, noMail.srv, "/settings", noMail.cookies)
	if noMailPage.Code != http.StatusOK {
		t.Fatalf("GET /settings (security server, no mail) status = %d, want 200", noMailPage.Code)
	}
	if strings.Contains(noMailPage.Body.String(), `href="/settings/email"`) {
		t.Errorf("settings page links to /settings/email although the outbox is not configured")
	}

	// 装配邮件（smtp_host + smtp_from 已设）：入口出现，且指向真实页面。
	withMail := newSecurityServer(t, true)
	page := getWithCookies(t, withMail.srv, "/settings", withMail.cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /settings (mail configured) status = %d, want 200 (body %s)", page.Code, snippet(page.Body.String()))
	}
	if !strings.Contains(page.Body.String(), `href="/settings/email"`) {
		t.Errorf("settings page does not link to /settings/email although the mail stack is enabled: %s", snippet(page.Body.String()))
	}

	// 入口确实可达：GET /settings/email 是已登录用户的真实页面（200）。
	target := getWithCookies(t, withMail.srv, "/settings/email", withMail.cookies)
	if target.Code != http.StatusOK {
		t.Fatalf("GET /settings/email status = %d, want 200 (body %s)", target.Code, snippet(target.Body.String()))
	}
}
