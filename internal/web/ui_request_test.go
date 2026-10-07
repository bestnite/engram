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
