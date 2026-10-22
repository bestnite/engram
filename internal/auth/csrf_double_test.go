package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// doubleSubmitRouter is a set of routes that does not depend on sessions: GET /form hands out the double-submit cookie and echoes the mirrored token,
// and POST /submit is validated only by PreSessionCSRFMiddleware. With it we can prove that pre-session CSRF needs no server-side session.
// secure is the value the caller passes after deciding whether the site is https (here the test supplies it directly).
func doubleSubmitRouter(secure bool) *gin.Engine {
	router := gin.New()
	router.Use(PreSessionCSRFMiddleware())
	router.GET("/form", func(c *gin.Context) {
		c.String(http.StatusOK, EnsureDoubleSubmitToken(c, secure))
	})
	router.POST("/submit", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return router
}

// TestDoubleSubmitIssuesCookieAndMirroredToken asserts that GET hands out both the cookie and a mirrorable token.
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

// TestDoubleSubmitRejections is the negative case: a missing cookie, a missing mirrored token and mismatched values must all 403.
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

// TestDoubleSubmitAcceptsMatchingPair asserts that a matching cookie and mirrored token pass (either the form field or the header).
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
