package web

import (
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
)

// TestPreSessionCSRFCookieSecureMatchesSiteScheme 断言登录前双提交 cookie 的 Secure 由
// BASE_URL 的 scheme 决定，而不是请求自身的 TLS 状态。
//
// 生产是「Caddy 终止 TLS → 应用只收到明文 http」，若按请求 TLS 判断，线上登录前表单下发的
// csrf_double 会缺失 Secure；浏览器不回传 http 站点上的 Secure cookie，反过来 http 开发站
// 若被标了 Secure 则登录前表单同样挂掉。httptest.NewRequest 不经过真实监听，TLS 恒为 nil，
// 正好模拟反代终止后的明文入站。
func TestPreSessionCSRFCookieSecureMatchesSiteScheme(t *testing.T) {
	cases := []struct {
		name       string
		baseURL    string
		wantSecure bool
	}{
		{name: "https site behind tls-terminating proxy", baseURL: "https://cards.example.com", wantSecure: true},
		{name: "https site with trailing slash", baseURL: "https://cards.example.com/", wantSecure: true},
		{name: "http development site", baseURL: "http://localhost:8080", wantSecure: false},
		{name: "unset base url (development default)", baseURL: "", wantSecure: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newAuthServer(t)
			srv.baseURL = tc.baseURL

			rec := get(t, srv, "/login", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /login status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
			}
			var cookie *http.Cookie
			for _, c := range rec.Result().Cookies() {
				if c.Name == auth.CSRFDoubleSubmitCookieName {
					cookie = c
				}
			}
			if cookie == nil {
				t.Fatalf("GET /login did not set the %s cookie", auth.CSRFDoubleSubmitCookieName)
			}
			if cookie.Secure != tc.wantSecure {
				t.Errorf("csrf_double Secure = %v, want %v (baseURL %q)", cookie.Secure, tc.wantSecure, tc.baseURL)
			}
			// Secure 之外的属性必须保持不变（HttpOnly + SameSite=Lax + Path=/）。
			if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
				t.Errorf("cookie flags = HttpOnly:%v SameSite:%v Path:%q, want HttpOnly:true SameSite:Lax Path:/",
					cookie.HttpOnly, cookie.SameSite, cookie.Path)
			}
		})
	}
}
