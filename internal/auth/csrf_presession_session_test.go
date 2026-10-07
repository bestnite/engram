package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件覆盖 PreSessionCSRFMiddleware 的**会话分支**：同一批「登录前可调用」的端点在调用者
// 已登录时必须改用会话绑定 token 校验，否则已登录用户（手里只有会话 token）会被双提交比对
// 一律拒绝——邮件里的验证/退订链接就是这么坏的。
//
// 夹具用一个把会话直接放进上下文的中间件替代真实会话中间件：被测中间件只读 CurrentSession
// 的判定结果，因此这样足以隔离它的行为。

// sessionCSRFRouter 装配一条「有会话」的路由：会话中间件放进给定的会话，再由被测中间件保护 POST。
func sessionCSRFRouter(sessionCSRF string) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ctxSessionKey), &store.Session{ID: "sess-1", UserID: 7, CSRFToken: sessionCSRF})
		c.Next()
	})
	router.Use(PreSessionCSRFMiddleware())
	router.POST("/submit", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return router
}

// TestPreSessionCSRFAcceptsSessionTokenWhenSignedIn 断言已登录请求带会话绑定 token 即放行
// （这正是修复前必然 403 的形态）。
func TestPreSessionCSRFAcceptsSessionTokenWhenSignedIn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const sessionToken = "session-bound-csrf-token-0123456789"

	for _, via := range []string{"header", "form"} {
		req := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader("csrf_token="+sessionToken))
		if via == "header" {
			req.Header.Set(CSRFHeaderName, sessionToken)
		} else {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		rec := httptest.NewRecorder()
		sessionCSRFRouter(sessionToken).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("via=%s: status = %d, want 200 (body %s)", via, rec.Code, rec.Body.String())
		}
	}
}

// TestPreSessionCSRFRejectsDoubleSubmitPairWhenSignedIn 是本改动的安全关键断言：
// 已登录时**不允许**退回双提交——即使 cookie 与请求值构成合法的一对，只要它不等于会话行里的值
// 就必须拒绝，否则「能往目标域写 cookie」的攻击者可以直接绕过（弱档只留给匿名）。
func TestPreSessionCSRFRejectsDoubleSubmitPairWhenSignedIn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const doubleSubmit = "double-submit-pair-value-0123456789"
	const sessionToken = "session-bound-csrf-token-0123456789"

	req := httptest.NewRequest(http.MethodPost, "/submit", nil)
	req.AddCookie(&http.Cookie{Name: CSRFDoubleSubmitCookieName, Value: doubleSubmit})
	req.Header.Set(CSRFHeaderName, doubleSubmit)
	rec := httptest.NewRecorder()
	sessionCSRFRouter(sessionToken).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("a matching double-submit pair must not pass while signed in: status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "csrf_failed") {
		t.Errorf("rejection should carry the stable csrf_failed code: %s", rec.Body.String())
	}
}

// TestPreSessionCSRFRejectsMissingOrWrongTokenWhenSignedIn 覆盖会话分支的其余拒绝路径。
func TestPreSessionCSRFRejectsMissingOrWrongTokenWhenSignedIn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const sessionToken = "session-bound-csrf-token-0123456789"

	cases := []struct {
		name   string
		header string
		cookie string
	}{
		{name: "no token at all"},
		{name: "wrong token", header: "some-other-token-0123456789"},
		{name: "short token", header: "short"},
		{name: "double-submit cookie present but header missing", cookie: "double-submit-pair-value-0123456789"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/submit", nil)
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: CSRFDoubleSubmitCookieName, Value: tc.cookie})
			}
			if tc.header != "" {
				req.Header.Set(CSRFHeaderName, tc.header)
			}
			rec := httptest.NewRecorder()
			sessionCSRFRouter(sessionToken).ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s: status = %d, want 403 (body %s)", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestPreSessionCSRFSafeMethodsPassWithSession 断言 GET 不受影响（写请求才校验）。
func TestPreSessionCSRFSafeMethodsPassWithSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ctxSessionKey), &store.Session{ID: "sess-1", UserID: 7, CSRFToken: "whatever-0123456789"})
		c.Next()
	})
	router.Use(PreSessionCSRFMiddleware())
	router.GET("/submit", func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/submit", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET status = %d, want 200", rec.Code)
	}
}
