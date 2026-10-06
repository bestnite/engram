package web

import (
	"net/url"
	"strings"
	"testing"
)

// TestHomeNoMathFormula 断言首页不再渲染演示公式（UI request 第 2 项：移除首页数学公式）。
func TestHomeNoMathFormula(t *testing.T) {
	srv := newSSRServer(t, nil)
	body := get(t, srv, "/", nil).Body.String()
	if strings.Contains(body, "a^2 + b^2") {
		t.Errorf("home page still renders the demo math formula: %s", snippet(body))
	}
}

// TestFooterLinksToRepository 断言页脚是居中链接且指向确切的源码仓库地址（UI request 第 3 项）。
// 首页（内联布局）与普通页面（pageLayout）都必须带上它。
func TestFooterLinksToRepository(t *testing.T) {
	const want = `href="https://git.nite07.com/nite/engram"`

	srv := newSSRServer(t, nil)
	if body := get(t, srv, "/", nil).Body.String(); !strings.Contains(body, want) {
		t.Errorf("home footer does not link to the repository: %s", snippet(body))
	}

	notesSrv, _, _, cookies, _ := newNotesServer(t)
	// GET /settings 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 回退设置页（DESIGN.md §8.5）。
	notesSrv.spa = nil
	if body := getWithCookies(t, notesSrv, "/settings", cookies).Body.String(); !strings.Contains(body, want) {
		t.Errorf("settings page footer does not link to the repository: %s", snippet(body))
	}
}

// TestLocaleURLPreservesPathAndQuery 断言语言链接保留当前路径与既有查询参数，只覆盖 lang
// （UI request 第 1 项）。
func TestLocaleURLPreservesPathAndQuery(t *testing.T) {
	tests := []struct {
		raw  string
		code string
		want string
	}{
		{raw: "/", code: "en", want: "/?lang=en"},
		{raw: "/decks?page=2", code: "en", want: "/decks?lang=en&page=2"},
		{raw: "/decks?page=2&lang=zh-CN", code: "en", want: "/decks?lang=en&page=2"},
	}
	for _, tc := range tests {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.raw, err)
		}
		if got := localeURL(u, tc.code); got != tc.want {
			t.Errorf("localeURL(%q, %q) = %q, want %q", tc.raw, tc.code, got, tc.want)
		}
	}
}

// TestLanguageSelectorOnPlainPage 断言普通页面外壳也渲染页头语言下拉（不只是首页），
// 且两种登录态渲染成两种形态：已登录是写库的 POST 表单，匿名是指向当前路径的 ?lang= 链接。
func TestLanguageSelectorOnPlainPage(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	// GET /settings 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 回退设置页（DESIGN.md §8.5）。
	srv.spa = nil
	body := getWithCookies(t, srv, "/settings", cookies).Body.String()
	if !strings.Contains(body, `action="/settings/locale"`) || !strings.Contains(body, `name="lang" value="en"`) {
		t.Errorf("signed-in settings page does not render the language form: %s", snippet(body))
	}

	anon := get(t, newSSRServer(t, nil), "/", nil).Body.String()
	if !strings.Contains(anon, `href="/?lang=en"`) {
		t.Errorf("anonymous home page does not render the ?lang= link: %s", snippet(anon))
	}
}

// TestSettingsNavVisibleToSignedInUsers 断言个人设置入口对已登录用户可见、匿名不可见
// （UI request 第 4 项）。它只加一个指向既有 /settings 的导航链接，不新增页面。
func TestSettingsNavVisibleToSignedInUsers(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
// 断言 SSR 页头导航里的 /settings 链接，显式走 SPA 缺失的回退分支。
	srv.spa = nil
	for _, path := range []string{"/", "/settings"} {
		body := getWithCookies(t, srv, path, cookies).Body.String()
		if !strings.Contains(body, `href="/settings"`) {
			t.Errorf("GET %s for a signed-in user does not link to /settings: %s", path, snippet(body))
		}
	}

	anon := get(t, newSSRServer(t, nil), "/", nil).Body.String()
	if strings.Contains(anon, `href="/settings"`) {
		t.Errorf("anonymous home page advertises /settings: %s", snippet(anon))
	}
}
