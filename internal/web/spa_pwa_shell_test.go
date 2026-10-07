package web

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

// spaShellBody 取一个 SPA 应用壳响应体（要求 200）。
func spaShellBody(t *testing.T, srv *Server, path string, headers map[string]string) string {
	t.Helper()
	rec := get(t, srv, path, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200; body %s", path, rec.Code, snippet(rec.Body.String()))
	}
	return rec.Body.String()
}

// TestSPAShellDeclaresPWAHead 断言 SPA 应用壳的 <head> 与 SSR 外壳一致：manifest、
// theme-color、内容哈希图标、/pwa.js 注册脚本与内联主题引导。
func TestSPAShellDeclaresPWAHead(t *testing.T) {
	srv := newRenderServer(t, nil)
	icon := srv.assets.URL("icons/icon.svg")
	apple := srv.assets.URL("icons/apple-touch-icon.png")
	if icon == "" || apple == "" {
		t.Fatalf("icon assets are not embedded: icon=%q apple=%q", icon, apple)
	}
	if !strings.HasPrefix(icon, staticPathPrefix) || !strings.HasPrefix(apple, staticPathPrefix) {
		t.Fatalf("icons must be content-hashed: icon=%q apple=%q", icon, apple)
	}

	body := spaShellBody(t, srv, "/", nil)
	for _, want := range []string{
		`<link rel="manifest" href="` + manifestPath + `"/>`,
		`<meta name="theme-color" content="#18181b"/>`,
		`<link rel="icon" type="image/svg+xml" href="` + icon + `"/>`,
		`<link rel="apple-touch-icon" href="` + apple + `"/>`,
		`<script src="` + pwaScriptPath + `"></script>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SPA shell is missing %q: %s", want, snippet(body))
		}
	}

	// 主题引导必须内联在 <head> 内、</head> 之前：外链脚本是独立请求，暗色下会先画一帧白底。
	head := strings.Index(body, "<head>")
	headEnd := strings.Index(body, "</head>")
	script := strings.Index(body, `localStorage.getItem("engram-theme")`)
	if head < 0 || headEnd < 0 || script < 0 || !(head < script && script < headEnd) {
		t.Errorf("inline theme bootstrap must sit in <head>: head=%d script=%d headEnd=%d", head, script, headEnd)
	}

	// 自托管：入口不得引用任何外部 URL。
	if strings.Contains(body, "http://") || strings.Contains(body, "https://") {
		t.Errorf("SPA shell references an external URL: %s", snippet(body))
	}
}

// TestSPAShellThemeBootstrapMatchesSSR 断言注入 SPA 外壳的内联引导与 SSR 用的是同一段
// 编译期常量：内容完全一致，CSP 的 script-src hash 白名单才无需放宽。
func TestSPAShellThemeBootstrapMatchesSSR(t *testing.T) {
	srv := newRenderServer(t, nil)
	body := spaShellBody(t, srv, "/", nil)

	start := strings.Index(body, "<script>(function(){var s;try{s=localStorage.getItem(\"engram-theme\")")
	if start < 0 {
		t.Fatal("SPA shell has no inline theme bootstrap <script>")
	}
	start += len("<script>")
	end := strings.Index(body[start:], "</script>")
	if end < 0 {
		t.Fatal("inline theme bootstrap has no closing </script>")
	}
	if inline := body[start : start+end]; inline != themeBootstrapJS {
		t.Errorf("SPA inline bootstrap differs from the SSR constant:\n got %q\nwant %q", inline, themeBootstrapJS)
	}
}

// TestSPAShellThemeBootstrapHashWhitelisted 从 SPA 外壳渲染结果里取出内联引导正文，独立算
// SHA-256，断言 CSP 带的正是它。hash 对不上时浏览器会把这唯一的内联 <script> 当未授权脚本
// 阻断，首屏主题引导即失效（与 SSR 的 TestSecurityHeadersCSPHashMatchesRenderedThemeBootstrap 同口径）。
func TestSPAShellThemeBootstrapHashWhitelisted(t *testing.T) {
	srv := newRenderServer(t, nil)
	rec := get(t, srv, "/", nil)
	csp := rec.Header().Get(wantCSPHeader)
	if csp == "" {
		t.Fatalf("%s is missing on the SPA shell", wantCSPHeader)
	}
	body := rec.Body.String()

	start := strings.Index(body, "<script>(function(){var s;try{s=localStorage.getItem(\"engram-theme\")")
	if start < 0 {
		t.Fatal("SPA shell has no inline theme bootstrap <script>")
	}
	start += len("<script>")
	end := strings.Index(body[start:], "</script>")
	if end < 0 {
		t.Fatal("inline theme bootstrap has no closing </script>")
	}
	inline := body[start : start+end]

	sum := sha256.Sum256([]byte(inline))
	want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	if !strings.Contains(csp, want) {
		t.Errorf("CSP %q does not carry %q, the hash of the SPA inline theme bootstrap", csp, want)
	}
}

// TestSPAShellLanguageFollowsI18nRules 断言 SPA 外壳的 <html lang> 按 i18n 优先级解析
// （?lang > 用户设置 > Accept-Language > 站点默认），与 SSR 一致。
func TestSPAShellLanguageFollowsI18nRules(t *testing.T) {
	srv := newRenderServer(t, nil)

	tests := []struct {
		name    string
		path    string
		headers map[string]string
		want    string
	}{
		{name: "site default", path: "/", want: `lang="zh-CN"`},
		{name: "lang query override", path: "/?lang=en", want: `lang="en"`},
		{name: "accept-language fallback", path: "/", headers: map[string]string{"Accept-Language": "en-US,en;q=0.9"}, want: `lang="en"`},
		{name: "unsupported lang falls back", path: "/?lang=de", want: `lang="zh-CN"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := spaShellBody(t, srv, tc.path, tc.headers)
			if !strings.Contains(body, `<html `+tc.want+`>`) {
				t.Errorf("GET %s shell does not declare %s: %s", tc.path, tc.want, snippet(body))
			}
		})
	}
}

// TestSPAShellETagTracksLanguage 断言入口 ETag 随语言变化：不同语言必须拿到不同 ETag，
// 否则换语言后的条件请求会命中旧壳（no-cache 语义）。
func TestSPAShellETagTracksLanguage(t *testing.T) {
	srv := newRenderServer(t, nil)

	zh := get(t, srv, "/", nil)
	en := get(t, srv, "/?lang=en", nil)
	if zh.Header().Get("ETag") == "" || en.Header().Get("ETag") == "" {
		t.Fatal("SPA shell ETag is missing")
	}
	if zh.Header().Get("ETag") == en.Header().Get("ETag") {
		t.Errorf("SPA shell ETag does not change with language: %q", zh.Header().Get("ETag"))
	}

	// 同语言的条件请求命中 304。
	cond := get(t, srv, "/", map[string]string{"If-None-Match": zh.Header().Get("ETag")})
	if cond.Code != http.StatusNotModified {
		t.Errorf("conditional GET / status = %d, want 304", cond.Code)
	}
	// 语言不同的条件请求不得命中 304，否则会拿到另一种语言的旧壳。
	cross := get(t, srv, "/?lang=en", map[string]string{"If-None-Match": zh.Header().Get("ETag")})
	if cross.Code != http.StatusOK {
		t.Errorf("cross-language conditional GET status = %d, want 200", cross.Code)
	}
}

// TestSetHTMLLangRewritesOpenTag 覆盖语言重写的边界：已有 lang 的替换、缺属性的补上、
// 其它属性保留、无 <html> 时原样返回、语言码转义。
func TestSetHTMLLangRewritesOpenTag(t *testing.T) {
	tests := []struct {
		name string
		in   string
		lang string
		want string
	}{
		{name: "replace existing", in: `<html lang="zh-CN"><body></body></html>`, lang: "en", want: `<html lang="en"><body></body></html>`},
		{name: "insert missing", in: `<html><body></body></html>`, lang: "en", want: `<html lang="en"><body></body></html>`},
		{name: "preserve other attrs", in: `<html class="x" lang="zh-CN">`, lang: "en", want: `<html class="x" lang="en">`},
		{name: "no html tag", in: `<div>no root</div>`, lang: "en", want: `<div>no root</div>`},
		{name: "escapes lang", in: `<html lang="zh-CN">`, lang: `en"onload`, want: `<html lang="en&#34;onload">`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(setHTMLLang([]byte(tc.in), tc.lang)); got != tc.want {
				t.Errorf("setHTMLLang(%q, %q) = %q, want %q", tc.in, tc.lang, got, tc.want)
			}
		})
	}
}

// TestBuildShellBlockSkipsEmptyAndEscapes 断言空字段不产出标记，属性值一律转义。
func TestBuildShellBlockSkipsEmptyAndEscapes(t *testing.T) {
	if block := buildShellBlock(SPAShell{}); len(block) != 0 {
		t.Errorf("empty SPAShell should inject nothing, got %q", block)
	}
	block := string(buildShellBlock(SPAShell{ManifestURL: `/m"x`}))
	if !strings.Contains(block, `href="/m&#34;x"`) {
		t.Errorf("shell attribute value is not escaped: %q", block)
	}
}

// TestSPAShellComposesWithMathJax 断言外壳注入与 MathJax 注入互不覆盖：两者都在 </head>
// 之前出现，且后设置的注入不会丢掉先前设置的那一项。
func TestSPAShellComposesWithMathJax(t *testing.T) {
	spa, err := NewSPA(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<html lang="zh-CN"><head></head><body><div id="app"></div></body></html>`)},
	})
	if err != nil {
		t.Fatalf("NewSPA() error = %v", err)
	}

	spa.SetMathJaxURL("/static/v/abcd1234/js/mathjax/tex-svg.js")
	spa.SetShell(SPAShell{
		ManifestURL:    manifestPath,
		ThemeColor:     "#18181b",
		ScriptURL:      pwaScriptPath,
		ThemeBootstrap: themeBootstrap,
	})

	got := string(spa.IndexHTML())
	headEnd := strings.Index(got, "</head>")
	for _, want := range []string{
		`<meta name="` + mathjaxMetaName + `" content="/static/v/abcd1234/js/mathjax/tex-svg.js" />`,
		`<link rel="manifest" href="` + manifestPath + `"/>`,
		`<script src="` + pwaScriptPath + `"></script>`,
		themeBootstrap,
	} {
		at := strings.Index(got, want)
		if at < 0 {
			t.Errorf("index is missing %q:\n%s", want, got)
			continue
		}
		if headEnd < 0 || at > headEnd {
			t.Errorf("%q was not injected inside <head>:\n%s", want, got)
		}
	}
}
