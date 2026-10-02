package web

import (
	"net/url"
	"strings"
	"testing"
)

// TestHomeNoMathFormula 断言首页不再渲染演示公式（UI request 第 2 项：移除首页数学公式）。
func TestHomeNoMathFormula(t *testing.T) {
	srv := newRenderServer(t, nil)
	body := get(t, srv, "/", nil).Body.String()
	if strings.Contains(body, "a^2 + b^2") {
		t.Errorf("home page still renders the demo math formula: %s", snippet(body))
	}
}

// TestFooterLinksToRepository 断言页脚是居中链接且指向确切的源码仓库地址（UI request 第 3 项）。
// 首页（内联布局）与普通页面（pageLayout）都必须带上它。
func TestFooterLinksToRepository(t *testing.T) {
	const want = `href="https://git.nite07.com/nite/engram"`

	srv := newRenderServer(t, nil)
	if body := get(t, srv, "/", nil).Body.String(); !strings.Contains(body, want) {
		t.Errorf("home footer does not link to the repository: %s", snippet(body))
	}

	notesSrv, _, _, cookies, _ := newNotesServer(t)
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

// TestLanguageSelectorOnPlainPage 断言普通页面外壳也渲染页头语言下拉（不只是首页）。
// 当前语言渲染为不可点的摘要，另一种语言渲染为指向当前路径的链接。
func TestLanguageSelectorOnPlainPage(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	body := getWithCookies(t, srv, "/settings", cookies).Body.String()
	if !strings.Contains(body, `href="/settings?lang=en"`) {
		t.Errorf("settings page header does not render the language dropdown: %s", snippet(body))
	}
}

// TestSettingsNavVisibleToSignedInUsers 断言个人设置入口对已登录用户可见、匿名不可见
// （UI request 第 4 项）。它只加一个指向既有 /settings 的导航链接，不新增页面。
func TestSettingsNavVisibleToSignedInUsers(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	for _, path := range []string{"/", "/settings"} {
		body := getWithCookies(t, srv, path, cookies).Body.String()
		if !strings.Contains(body, `href="/settings"`) {
			t.Errorf("GET %s for a signed-in user does not link to /settings: %s", path, snippet(body))
		}
	}

	anon := get(t, newRenderServer(t, nil), "/", nil).Body.String()
	if strings.Contains(anon, `href="/settings"`) {
		t.Errorf("anonymous home page advertises /settings: %s", snippet(anon))
	}
}
