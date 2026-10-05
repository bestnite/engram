package web

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 本文件是 F26 的验收：一组固定安全响应头挂在所有路由上，其中 CSP 目前只上报不阻断。
// 断言一律用字面量，不用被测包里的常量，避免「常量改了测试跟着改」自证。
const (
	wantCSPReportOnlyHeader = "Content-Security-Policy-Report-Only"
	wantReferrerPolicy      = "strict-origin-when-cross-origin"
)

// assertSecurityHeaders 断言五组响应头齐全，且 CSP 处于 Report-Only。
func assertSecurityHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	h := rec.Header()

	csp := h.Get(wantCSPReportOnlyHeader)
	if csp == "" {
		t.Errorf("%s is missing", wantCSPReportOnlyHeader)
	}
	// F26 明确不切强制模式：阻断头一旦出现就是误上。
	if got := h.Get("Content-Security-Policy"); got != "" {
		t.Errorf("enforcing Content-Security-Policy must not be set in F26; got %q", got)
	}
	for _, d := range []string{"default-src", "frame-ancestors"} {
		if !strings.Contains(csp, d) {
			t.Errorf("%s = %q, missing directive %q", wantCSPReportOnlyHeader, csp, d)
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

	asset := srv.assets.URL("js/htmx.min.js")
	if asset == "" {
		t.Fatal("htmx asset is not embedded")
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

// TestSecurityHeadersDoNotAlterResponseBody 断言 Report-Only 不阻断：登录页与复习页
// 仍是 200 与各自的真实内容，中间件只加头、不改状态码与响应体。
func TestSecurityHeadersDoNotAlterResponseBody(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "CSP body deck")
	seedBasic(t, db, deck.ID, "FrontCSP", "BackCSP")

	tests := []struct {
		name    string
		path    string
		cookies []*http.Cookie
		want    []string
	}{
		{name: "login page", path: "/login", want: []string{"<form", "csrf_token"}},
		{name: "review page", path: "/review?deck=" + u64str(deck.ID), cookies: cookies, want: []string{"review-area", "FrontCSP"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := getWithCookies(t, srv, tc.path, tc.cookies)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want 200 (report-only must not block; body %s)", tc.path, rec.Code, snippet(rec.Body.String()))
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
	csp := getWithCookies(t, srv, "/login", nil).Header().Get(wantCSPReportOnlyHeader)
	if csp == "" {
		t.Fatalf("%s is missing", wantCSPReportOnlyHeader)
	}

	for _, want := range []string{
		"default-src 'self'",
		"script-src 'self'",
		"'unsafe-eval'",                    // htmx 用 new Function 编译 hx-on
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
// 内联 <script> 当未授权脚本持续上报，策略等于没生效。
func TestSecurityHeadersCSPHashMatchesRenderedThemeBootstrap(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/login", nil)
	csp := rec.Header().Get(wantCSPReportOnlyHeader)

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
