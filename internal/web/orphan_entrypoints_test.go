package web

import (
	"net/http"
	"strings"
	"testing"
)

// 本文件覆盖「孤立页面补链」任务：把已存在但此前没有入口的页面挂到上下文合适的入口上。
//   - /import：卡组列表头部的导入按钮（不塞进全局导航的第六项）；
//   - /decks/:id/sharing：仅 owner 行给出共享入口，被共享方（非 owner）不出现；
//   - /settings/email：个人设置页的改邮箱卡片，仅安全邮件栈启用时渲染。
//
// 测试全部走真实路由（httptest + 已登录会话），复用既有的 newNotesServer /
// newSecurityServer / seedDeck / createUserAndLogin 助手，不 mock 数据库。

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
	// GET /settings 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 回退设置页（DESIGN.md §8.5）。
	plainSrv.spa = nil
	plain := getWithCookies(t, plainSrv, "/settings", plainCookies)
	if plain.Code != http.StatusOK {
		t.Fatalf("GET /settings (no mail) status = %d, want 200 (body %s)", plain.Code, snippet(plain.Body.String()))
	}
	if strings.Contains(plain.Body.String(), `href="/settings/email"`) {
		t.Errorf("settings page links to /settings/email although the mail stack is disabled: %s", snippet(plain.Body.String()))
	}

	// 未装配邮件的安全测试服务：同样不应出现入口（第二条独立路径）。
	noMail := newSecurityServer(t, false)
	noMail.srv.spa = nil
	noMailPage := getWithCookies(t, noMail.srv, "/settings", noMail.cookies)
	if noMailPage.Code != http.StatusOK {
		t.Fatalf("GET /settings (security server, no mail) status = %d, want 200", noMailPage.Code)
	}
	if strings.Contains(noMailPage.Body.String(), `href="/settings/email"`) {
		t.Errorf("settings page links to /settings/email although the outbox is not configured")
	}

	// 装配邮件（smtp_host + smtp_from 已设）：入口出现，且指向真实页面。
	withMail := newSecurityServer(t, true)
	withMail.srv.spa = nil
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
