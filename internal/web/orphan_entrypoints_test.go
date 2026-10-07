package web

import (
	"net/http"
	"strings"
	"testing"
)

// 本文件覆盖「孤立页面补链」任务里在 SPA 迁移后仍然成立的部分：入口页本身可达且仍受登录保护。
//   - /decks：匿名访问仍重定向到登录页，因此它的导入入口不会泄漏给未登录访客；
//   - /settings/email：登录用户可达（返回应用壳），匿名仍先重定向登录。
//
// SSR 设置页此前按邮件栈是否启用条件渲染 /settings/email 卡片；页面层删除后，该条件由前端
// 视图决定，Go 侧不再渲染这张卡片，对应断言随之删除。测试全部走真实路由（httptest + 已登录会话）。

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

// TestSettingsEmailChangeRouteReachable 断言改邮箱入口页 /settings/email 仍是登录用户可达的
// 真实页面（返回应用壳），且匿名访问仍先被重定向到登录页。页面内是否展示改邮箱卡片由前端决定。
func TestSettingsEmailChangeRouteReachable(t *testing.T) {
	ts := newSecurityServer(t, false)

	page := getWithCookies(t, ts.srv, "/settings/email", ts.cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /settings/email status = %d, want 200 (body %s)", page.Code, snippet(page.Body.String()))
	}
	if !strings.Contains(page.Body.String(), `id="app"`) {
		t.Errorf("GET /settings/email did not serve the SPA shell: %s", snippet(page.Body.String()))
	}

	anon := getWithCookies(t, ts.srv, "/settings/email", nil)
	if anon.Code != http.StatusSeeOther {
		t.Fatalf("anonymous GET /settings/email status = %d, want 303", anon.Code)
	}
	if loc := anon.Header().Get("Location"); loc != "/login" {
		t.Errorf("anonymous GET /settings/email Location = %q, want /login", loc)
	}
}
