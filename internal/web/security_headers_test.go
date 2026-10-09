package web

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/frontend"
)

// 本文件是验收用例：一组固定安全响应头挂在所有路由上，CSP 已从 Report-Only 切成
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
		{name: "review page", path: "/review?deck=" + deck.PublicID, cookies: cookies},
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
		{name: "review page", path: "/review?deck=" + deck.PublicID, cookies: cookies, want: []string{shellMarker}},
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
		"style-src 'self' 'unsafe-inline'", // 内联 style= 属性（统计页柱宽、骨架屏宽度）
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

	// 交付的脚本没有一个在运行时编译 JS（守护用例对自有脚本断言这一点），策略带
	// 'unsafe-eval' 等于白白把 script-src 重新打开。
	if scriptSrc := cspDirectiveValue(csp, "script-src"); strings.Contains(scriptSrc, "'unsafe-eval'") {
		t.Errorf("script-src must not carry 'unsafe-eval': no served script compiles JS at runtime: %q", scriptSrc)
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

// cspStripLineComments 丢掉以 // 开头的整行注释。脚本与应用壳标记里都有注释，而注释正文
// 里就可能出现 "on*=" 或 "eval" 一类的字样——不先剔除，守护用例会对注释误报。
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

// cspInlineScriptBodies 抽出不带 src 的 <script> 正文：标签含 src= 的算外链、跳过；
// 其余标签的正文返回给调用方独立算哈希。
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
	// cspRuntimeCompile 命中运行时编译 JS 的写法（eval / new Function）。它们依赖
	// script-src 'unsafe-eval'，而策略刻意不写：自有脚本一旦引入，用例就变红，
	// 逼作者先想清楚是放宽策略还是换实现。
	cspRuntimeCompile = regexp.MustCompile(`\beval\s*\(|new\s+Function\s*\(`)
	// cspExternalResource 命中 src= / href= 里的绝对地址（http(s): 或协议相对 //）。
	// 策略只给了 'self' 与主题引导的 hash，多一个来源都会被浏览器阻断。
	cspExternalResource = regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["'](?:https?:)?//[^"']*`)
)

// TestSecurityHeadersGuardCoversServedShellAndScripts 是代替「人工逐页走查」的静态守护用例。
//
// 它把真正会送到浏览器的来源抽出来，与「实际下发的策略」逐条对照：
//
//   - 渲染出的 SPA 应用壳（用例直接取响应体）与 Go 现场生成的 /sw.js 里的内联 <script>
//     （无 src）→ 其 SHA-256 必须出现在 script-src（hash 放行）；
//   - 同一批来源里的 <style> 块或 style= 属性 → style-src 必须含 'unsafe-inline'
//     （style 属性无法被 hash/nonce 覆盖，理由见 security_headers.go）；
//   - 事件属性 on*= → script-src 必须含 'unsafe-inline'（事件属性同样无法 hash）；
//   - 这两处来源里的 eval / new Function → 需要 script-src 'unsafe-eval'，而策略刻意不写，
//     用例因此变红；
//   - 应用壳里的外链脚本/样式必须同源：策略只给了 'self' 与主题引导的 hash，多一个来源
//     （CDN、独立静态域名）都会被浏览器阻断；
//   - 构建期生成的入口 index.html → 必须没有内联 <script>：服务端只给应用壳补 hash，
//     不会给构建产物补，一旦出现就是只在线上暴露的阻断。
//
// 任何「实际在用、策略没放行」的组合都会让用例变红。它拦不住的两类必须靠浏览器冒烟：
// 运行时由脚本动态插入 DOM 的资源，以及压缩过的打包产物内部（/assets/index-*.js 不参与
// 文本匹配）。
func TestSecurityHeadersGuardCoversServedShellAndScripts(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	shell := getWithCookies(t, srv, "/login", nil)
	csp := shell.Header().Get(wantCSPHeader)
	if csp == "" {
		t.Fatalf("%s is missing", wantCSPHeader)
	}
	scriptSrc := cspDirectiveValue(csp, "script-src")
	styleSrc := cspDirectiveValue(csp, "style-src")
	if scriptSrc == "" || styleSrc == "" {
		t.Fatalf("CSP %q is missing script-src or style-src", csp)
	}

	shellBody := shell.Body.String()
	sw := getWithCookies(t, srv, "/sw.js", nil).Body.String()
	if !strings.Contains(sw, "STATIC_CACHE") {
		t.Fatalf("/sw.js did not serve the generated service worker: %s", snippet(sw))
	}
	// 只核对运行时真的会送到浏览器的两处来源：渲染出的应用壳与现场生成的 /sw.js。
	sources := map[string]string{"rendered SPA shell": shellBody, "/sw.js": sw}

	for name, raw := range sources {
		src := cspStripLineComments(raw)

		for _, body := range cspInlineScriptBodies(src) {
			sum := sha256.Sum256([]byte(body))
			token := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
			if !strings.Contains(scriptSrc, token) {
				t.Errorf("%s: inline <script> with hash %s is not whitelisted by script-src %q", name, token, scriptSrc)
			}
		}
		if cspStyleUse.MatchString(src) && !strings.Contains(styleSrc, "'unsafe-inline'") {
			t.Errorf("%s: inline style (<style> or style=) is used but style-src %q lacks 'unsafe-inline'", name, styleSrc)
		}
		if cspEventAttr.MatchString(src) && !strings.Contains(scriptSrc, "'unsafe-inline'") {
			t.Errorf("%s: inline event attribute on*= is used but script-src %q lacks 'unsafe-inline'", name, scriptSrc)
		}
		if cspRuntimeCompile.MatchString(src) {
			t.Errorf("%s: eval / new Function needs script-src 'unsafe-eval', which the policy deliberately omits: %q", name, scriptSrc)
		}
	}

	for _, ref := range cspExternalResource.FindAllString(shellBody, -1) {
		t.Errorf("rendered SPA shell references a cross-origin resource (%s); the policy only allows 'self'", ref)
	}

	// 入口由 Vite 生成，服务端只在它上面注入外壳标记，不会为构建期出现的内联脚本补 hash。
	dist, err := frontend.FS()
	if err != nil {
		t.Fatalf("frontend.FS: %v", err)
	}
	entry, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		t.Fatalf("read embedded dist/index.html: %v", err)
	}
	if bodies := cspInlineScriptBodies(string(entry)); len(bodies) != 0 {
		t.Errorf("embedded dist/index.html carries %d inline <script> block(s) that no CSP hash covers", len(bodies))
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
