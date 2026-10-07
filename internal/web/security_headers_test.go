package web

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 本文件是 F26 的验收：一组固定安全响应头挂在所有路由上，CSP 已从 Report-Only 切成
// 强制模式。断言一律用字面量，不用被测包里的常量，避免「常量改了测试跟着改」自证。
const (
	wantCSPHeader      = "Content-Security-Policy"
	wantCSPReportOnly  = "Content-Security-Policy-Report-Only"
	wantReferrerPolicy = "strict-origin-when-cross-origin"
)

// assertSecurityHeaders 断言五组响应头齐全，且 CSP 是强制头（阻断模式）。
func assertSecurityHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	h := rec.Header()

	csp := h.Get(wantCSPHeader)
	if csp == "" {
		t.Errorf("%s is missing", wantCSPHeader)
	}
	// 强制模式：不能同时下发 Report-Only——两个 CSP 头互相覆盖，浏览器只认最后一个，
	// 混发会让策略不可预期。
	if got := h.Get(wantCSPReportOnly); got != "" {
		t.Errorf("%s must not be set once the policy is enforcing; got %q", wantCSPReportOnly, got)
	}
	for _, d := range []string{"default-src", "frame-ancestors"} {
		if !strings.Contains(csp, d) {
			t.Errorf("%s = %q, missing directive %q", wantCSPHeader, csp, d)
		}
	}
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := h.Get("Referrer-Policy"); got != wantReferrerPolicy {
		t.Errorf("Referrer-Policy = %q, want %q", got, wantReferrerPolicy)
	}
	if got := h.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := h.Get("Permissions-Policy"); got == "" {
		t.Error("Permissions-Policy is missing")
	}
}

// TestSecurityHeadersCoverRepresentativeRoutes 覆盖登录页、复习页，以及静态资源、
// 健康检查、/api/v1 与 /mcp 前缀和 404 回退。中间件挂全局，这几条出口都不能漏。
func TestSecurityHeadersCoverRepresentativeRoutes(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "CSP deck")
	seedBasic(t, db, deck.ID, "FrontCSP", "BackCSP")

	asset := srv.assets.URL("js/pwa.js")
	if asset == "" {
		t.Fatal("pwa asset is not embedded")
	}

	tests := []struct {
		name    string
		path    string
		cookies []*http.Cookie
	}{
		{name: "login page", path: "/login"},
		{name: "review page", path: "/review?deck=" + u64str(deck.ID), cookies: cookies},
		{name: "home page", path: "/"},
		{name: "static asset", path: asset},
		{name: "health", path: "/healthz"},
		{name: "api prefix", path: "/api/v1/ping"},
		{name: "mcp endpoint", path: "/mcp"},
		{name: "unknown path", path: "/no-such-page"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := getWithCookies(t, srv, tc.path, tc.cookies)
			assertSecurityHeaders(t, rec)
		})
	}
}

// TestSecurityHeadersDoNotAlterResponseBody 断言中间件只加头、不改状态码与响应体：
// 登录页与复习页仍是 200 且都返回真实的 SPA 应用壳（而非被中间件截断）。
func TestSecurityHeadersDoNotAlterResponseBody(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "CSP body deck")
	seedBasic(t, db, deck.ID, "FrontCSP", "BackCSP")

	const shellMarker = `<div id="app">`
	tests := []struct {
		name    string
		path    string
		cookies []*http.Cookie
		want    []string
	}{
		{name: "login page", path: "/login", want: []string{shellMarker}},
		{name: "review page", path: "/review?deck=" + u64str(deck.ID), cookies: cookies, want: []string{shellMarker}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := getWithCookies(t, srv, tc.path, tc.cookies)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want 200; body %s", tc.path, rec.Code, snippet(rec.Body.String()))
			}
			body := rec.Body.String()
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("%s body lost %q: %s", tc.path, want, snippet(body))
				}
			}
		})
	}
}

// TestSecurityHeadersCSPMatchesPageResources 逐指令钉住 CSP：每条取值都能对应到实际
// 页面资源（模板与静态资源的证据见 security_headers.go 的注释）。
func TestSecurityHeadersCSPMatchesPageResources(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	csp := getWithCookies(t, srv, "/login", nil).Header().Get(wantCSPHeader)
	if csp == "" {
		t.Fatalf("%s is missing", wantCSPHeader)
	}

	for _, want := range []string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'", // 内联 style= 属性（stats.templ）
		"img-src 'self' https:",            // /media 与卡面允许的 https 外链图
		"connect-src 'self'",
		"object-src 'none'",
		"frame-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP = %q, missing %q", csp, want)
		}
	}

	// hx-on 已改成委托监听（notes.js），策略不应再需要 'unsafe-eval'：htmx 一旦用它
	// 编译 JS 表达式就必须 eval，留着等于把 script-src 重新打开。
	if scriptSrc := cspDirectiveValue(csp, "script-src"); strings.Contains(scriptSrc, "'unsafe-eval'") {
		t.Errorf("script-src must not carry 'unsafe-eval' now that hx-on is gone: %q", scriptSrc)
	}

	// 页面唯一的 inline <script>（主题引导）必须用 hash 放行；nonce 或 script-src
	// 'unsafe-inline' 都会让「防内联脚本」失去意义。
	if !strings.Contains(csp, "'sha256-") {
		t.Errorf("CSP does not whitelist the inline theme bootstrap by hash: %q", csp)
	}
	if strings.Contains(csp, "'nonce-") {
		t.Errorf("CSP uses a nonce but the inline script is a compile-time constant: %q", csp)
	}
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Errorf("script-src must not blanket-allow inline scripts: %q", csp)
	}
}

// TestSecurityHeadersCSPHashMatchesRenderedThemeBootstrap 从渲染结果里取出内联主题引导
// 脚本的正文，独立算 SHA-256，断言 CSP 带的正是它。hash 对不上时浏览器会把这唯一的
// 内联 <script> 当未授权脚本阻断，策略等于把首屏主题引导打掉。
func TestSecurityHeadersCSPHashMatchesRenderedThemeBootstrap(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/login", nil)
	csp := rec.Header().Get(wantCSPHeader)

	body := rec.Body.String()
	const open = `<script>(function(){var s;try{s=localStorage.getItem("engram-theme")`
	start := strings.Index(body, open)
	if start < 0 {
		t.Fatal("rendered page has no inline theme bootstrap <script>")
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
		t.Errorf("CSP %q does not carry %q, the hash of the rendered inline theme bootstrap", csp, want)
	}
}

// ---- 静态守护：策略必须覆盖模板与脚本里的实际用法 ----

// cspGuardedFiles 返回守护用例要扫描的源码清单：internal/web/views/*.templ 与
// internal/web/static/js/*.js，排除打包产物（*.min.js）与 mathjax 子目录
// （filepath.Glob 的 * 不跨目录，mathjax 天然被排除）。测试的工作目录就是
// internal/web，故用相对路径。
func cspGuardedFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, pattern := range []string{"views/*.templ", "static/js/*.js"} {
		matched, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		for _, f := range matched {
			if strings.HasSuffix(f, ".min.js") {
				continue
			}
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		t.Fatal("no template or script files matched; the guard would pass vacuously")
	}
	return files
}

// cspStripLineComments 丢掉以 // 开头的整行注释。templ 与 JS 源码里都有注释，而
// base.templ 的注释正文里就写着 "<script>"、review.js 的注释里写着 "hx-on"——不先
// 剔除，守护用例会对注释误报。
func cspStripLineComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			b.WriteString("\n")
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// cspInlineScriptBodies 抽出不带 src 的 <script> 正文。templ 源不是 HTML，这里只做
// 最小组装：标签含 src= 的算外链、跳过；其余标签的正文返回给调用方独立算哈希。
func cspInlineScriptBodies(src string) []string {
	const closeTag = "</script>"
	var out []string
	for {
		i := strings.Index(src, "<script")
		if i < 0 {
			return out
		}
		rest := src[i:]
		open := strings.Index(rest, ">")
		if open < 0 {
			return out
		}
		tag := rest[:open+1]
		body := rest[open+1:]
		end := strings.Index(body, closeTag)
		if end < 0 {
			return out
		}
		if !strings.Contains(tag, "src=") {
			out = append(out, body[:end])
		}
		src = body[end+len(closeTag):]
	}
}

var (
	// cspStyleUse 命中内联 <style> 块或 style= 属性，两者都由 style-src 管控。
	// 要求 style 前是空白或引号，避免误伤 data-style= 一类的复合属性名。
	cspStyleUse = regexp.MustCompile(`(?:^|[\s"'])style\s*=|(?i)<style[\s>]`)
	// cspEventAttr 命中 HTML 事件属性 on*=。要求前导是空白/引号/尖括号，避免误伤
	// JS 里的属性赋值（形如 btn.onclick = fn，前导是点号）。
	cspEventAttr = regexp.MustCompile(`(?:^|[\s"'<>])on[a-z]+\s*=`)
	// cspHTMXEvalUse 命中 htmx 需要运行时编译 JS 的写法：hx-on（含 hx-on: 与 hx-on-）
	// 与 hx-vals / hx-trigger 值里的 js: 表达式。它们执行时都走 new Function/Function，
	// 依赖 script-src 'unsafe-eval'。
	cspHTMXEvalUse = regexp.MustCompile(`hx-on|hx-(?:vals|trigger)\s*=\s*["'][^"']*js:`)
)

// TestSecurityHeadersGuardCoversTemplAndScripts 是代替「人工逐页走查」的静态守护用例。
//
// 它把 internal/web/views/*.templ 与 internal/web/static/js/*.js（排除打包产物与
// mathjax）里会触达 CSP 的写法抽出来，与「实际下发的策略」逐条对照：
//
//   - 内联 <script>（无 src）→ 其 SHA-256 必须出现在 script-src（hash 放行）；
//   - <style> 块或 style= 属性 → style-src 必须含 'unsafe-inline'（style 属性无法
//     被 hash 覆盖，理由见 security_headers.go）；
//   - 事件属性 on*= → script-src 必须含 'unsafe-inline'（事件属性同样无法 hash）；
//   - hx-on / hx-*="js:…" → script-src 必须含 'unsafe-eval'（htmx 用 new Function 编译）。
//
// 任何「源码用了、策略没放行」的组合都会让用例变红——典型场景是未来有人往模板里塞
// 一段新的内联 <script>，而策略没有对应的 hash/nonce。
//
// 它拦不住的东西（必须靠浏览器冒烟）：运行时由脚本动态插入 DOM 的资源
// （如 htmx 自己注入的指示器 <style>、matchMedia 的响应用法）、由 Go 常量经 rawHTML
// 输出的 inline 资源（如 decks_data.go 的 noscriptDialogStyle、themeBootstrap——后者
// 由 TestSecurityHeadersCSPHashMatchesRenderedThemeBootstrap 单独用渲染结果兜住）。
func TestSecurityHeadersGuardCoversTemplAndScripts(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	csp := getWithCookies(t, srv, "/login", nil).Header().Get(wantCSPHeader)
	if csp == "" {
		t.Fatalf("%s is missing", wantCSPHeader)
	}
	scriptSrc := cspDirectiveValue(csp, "script-src")
	styleSrc := cspDirectiveValue(csp, "style-src")
	if scriptSrc == "" || styleSrc == "" {
		t.Fatalf("CSP %q is missing script-src or style-src", csp)
	}

	for _, path := range cspGuardedFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := cspStripLineComments(string(raw))

		for _, body := range cspInlineScriptBodies(src) {
			sum := sha256.Sum256([]byte(body))
			token := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
			if !strings.Contains(scriptSrc, token) {
				t.Errorf("%s: inline <script> with hash %s is not whitelisted by script-src %q", path, token, scriptSrc)
			}
		}
		if cspStyleUse.MatchString(src) && !strings.Contains(styleSrc, "'unsafe-inline'") {
			t.Errorf("%s: inline style (<style> or style=) is used but style-src %q lacks 'unsafe-inline'", path, styleSrc)
		}
		if cspEventAttr.MatchString(src) && !strings.Contains(scriptSrc, "'unsafe-inline'") {
			t.Errorf("%s: inline event attribute on*= is used but script-src %q lacks 'unsafe-inline'", path, scriptSrc)
		}
		if cspHTMXEvalUse.MatchString(src) && !strings.Contains(scriptSrc, "'unsafe-eval'") {
			t.Errorf("%s: htmx JS-expression attribute (hx-on / js:) needs 'unsafe-eval' but script-src %q lacks it", path, scriptSrc)
		}
	}
}

// cspDirectiveValue 从完整策略里取出某条指令的值；缺该指令时返回空串。
func cspDirectiveValue(csp, name string) string {
	for _, part := range strings.Split(csp, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, name+" ") {
			return strings.TrimPrefix(part, name+" ")
		}
	}
	return ""
}
