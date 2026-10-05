package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// doubleSubmitRouter 是不依赖会话的一套路由：GET /form 下发双提交 cookie 并回显镜像 token，
// POST /submit 只由 DoubleSubmitMiddleware 校验。用它可以证明会话前 CSRF 不依赖服务端会话。
// secure 是调用方判定「站点是否 https」后传入的值（这里由测试直接给定）。
func doubleSubmitRouter(secure bool) *gin.Engine {
	router := gin.New()
	router.Use(DoubleSubmitMiddleware())
	router.GET("/form", func(c *gin.Context) {
		c.String(http.StatusOK, EnsureDoubleSubmitToken(c, secure))
	})
	router.POST("/submit", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return router
}

// TestDoubleSubmitIssuesCookieAndMirroredToken 断言 GET 同时下发 cookie 与可镜像的 token。
func TestDoubleSubmitIssuesCookieAndMirroredToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	doubleSubmitRouter(false).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/form", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /form status = %d, want 200", rec.Code)
	}
	token := rec.Body.String()
	if !validDoubleSubmitToken(token) {
		t.Fatalf("mirrored token %q is not usable", token)
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == CSRFDoubleSubmitCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("GET /form did not set the double-submit cookie")
	}
	if cookie.Value != token {
		t.Errorf("cookie value %q != mirrored token %q", cookie.Value, token)
	}
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie flags = HttpOnly:%v SameSite:%v, want HttpOnly:true SameSite:Lax", cookie.HttpOnly, cookie.SameSite)
	}
}

// TestDoubleSubmitRejections 是 B-13 的反面用例：缺 cookie、缺镜像 token、值不一致都必须 403。
func TestDoubleSubmitRejections(t *testing.T) {
	cases := []struct {
		name   string
		cookie string
		token  string
	}{
		{name: "no cookie and no token"},
		{name: "token without cookie", token: "mirrored-token-0123456789"},
		{name: "cookie without mirrored token", cookie: "cookie-value-0123456789"},
		{name: "mismatched cookie and token", cookie: "cookie-value-0123456789", token: "other-value-0123456789"},
		{name: "short cookie rejected", cookie: "short", token: "short"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(""))
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: CSRFDoubleSubmitCookieName, Value: tc.cookie})
			}
			if tc.token != "" {
				req.Header.Set(CSRFHeaderName, tc.token)
			}
			rec := httptest.NewRecorder()
			doubleSubmitRouter(false).ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s: status = %d, want 403", tc.name, rec.Code)
			}
		})
	}
}

// TestDoubleSubmitAcceptsMatchingPair 断言 cookie 与镜像 token 一致时放行（表单字段与头部均可)。
func TestDoubleSubmitAcceptsMatchingPair(t *testing.T) {
	const tok = "matching-double-submit-token-0123456789"
	for _, form := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader("csrf_token="+tok))
		if form {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			req.Header.Set(CSRFHeaderName, tok)
		}
		req.AddCookie(&http.Cookie{Name: CSRFDoubleSubmitCookieName, Value: tok})
		rec := httptest.NewRecorder()
		doubleSubmitRouter(false).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("form=%v: status = %d, want 200 (body %s)", form, rec.Code, rec.Body.String())
		}
	}
}
