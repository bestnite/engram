package web

import (
	"strings"
	"testing"
)

// TestThemeBootstrapRunsBeforeStylesheet 是验收：主题引导必须内联在 <head> 内。
// 外链的 pwa.js 与样式表都是独立请求，浏览器可能在它们到达前先画出一帧白底；内联引导把暗色类、
// color-scheme、画布底色提前到首帧之前。
//
// 页面层只剩 SPA 应用壳：引导由 server.go 在装配期注入入口 <head>（SPA.SetShell），
// 本用例断言它在壳里原样出现且未转义。
func TestThemeBootstrapRunsBeforeStylesheet(t *testing.T) {
	srv := newRenderServer(t, nil)
	body := get(t, srv, "/", nil).Body.String()

	head := strings.Index(body, "<head>")
	if head < 0 {
		t.Fatalf("rendered shell has no <head>: %s", snippet(body))
	}
	script := strings.Index(body, `localStorage.getItem("engram-theme")`)
	if script < 0 {
		t.Fatalf("head has no inline theme bootstrap (engram-theme lookup): %s", snippet(body))
	}
	if headEnd := strings.Index(body, "</head>"); headEnd >= 0 && script > headEnd {
		t.Errorf("theme bootstrap must sit inside <head>: head=%d script=%d headEnd=%d", head, script, headEnd)
	}

	// 逐项断言引导必须覆盖的内容：存储键、color-scheme、暗色画布底色，以及系统主题回退。
	for _, want := range []string{
		`engram-theme`,
		`colorScheme`,
		`#09090b`,
		`#f8fafc`,
		`(prefers-color-scheme: dark)`,
		`classList.toggle("dark"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("theme bootstrap is missing %q: %s", want, snippet(body))
		}
	}

	// 原样输出说明注入未转义；被 HTML 转义时（&#34; 等）不会匹配到常量本身。
	if !strings.Contains(body, themeBootstrap) {
		t.Errorf("theme bootstrap is not rendered verbatim (escaped?): %s", snippet(body))
	}
	// 再钉一遍“未转义”：这段含引号的原始 JS 只可能出现在未转义的脚本内容里。
	if !strings.Contains(body, `r.style.backgroundColor=d?"#09090b":"#f8fafc"`) {
		t.Errorf("theme bootstrap script content looks HTML-escaped: %s", snippet(body))
	}
}
