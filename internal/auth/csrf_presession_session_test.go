package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// This file covers the **session branch** of PreSessionCSRFMiddleware: the same batch of "callable before login" endpoints must switch to
// session-bound token validation when the caller is logged in, otherwise a logged-in user (holding only a session token) would be rejected
// outright by the double-submit comparison — this is exactly how the verification/unsubscribe links in mail broke.
//
// The fixture replaces the real session middleware with one that puts a session straight into the context: the middleware under test only reads
// the verdict of CurrentSession, so that is enough to isolate its behavior.

// sessionCSRFRouter wires a route "with a session": the session middleware installs the given session, then the middleware under test protects the POST.
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

// TestPreSessionCSRFAcceptsSessionTokenWhenSignedIn asserts that a signed-in request carrying a session-bound token passes
// (which is exactly the shape that necessarily got 403 before the fix).
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

// TestPreSessionCSRFRejectsDoubleSubmitPairWhenSignedIn is the security-critical assertion of this change:
// when signed in, falling back to double submit is **not allowed** — even if the cookie and the request value form a valid pair, as long as it does not equal
// the value in the session row it must be rejected, otherwise an attacker who "can write cookies for the target domain" could bypass it directly (the weak tier is reserved for anonymous requests).
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

// TestPreSessionCSRFRejectsMissingOrWrongTokenWhenSignedIn covers the remaining rejection paths of the session branch.
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

// TestPreSessionCSRFSafeMethodsPassWithSession asserts that GET is unaffected (only write requests are validated).
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
