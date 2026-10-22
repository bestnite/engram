package auth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// csrfRouter is a set of routes with the CSRF middleware; the session middleware must come first so the token has a source.
func csrfRouter(e *testEnv) *gin.Engine {
	return e.buildRouter(e.manager.Middleware(), e.manager.CSRFMiddleware())
}

// newPostRequest builds a POST carrying a session cookie; with form=true the token rides in the form field, otherwise in the header.
func newPostRequest(t *testing.T, e *testEnv, cookieValue string, form bool, token string) *http.Request {
	t.Helper()
	var body io.Reader
	if form {
		body = strings.NewReader("csrf_token=" + url.QueryEscape(token))
	}
	req := httptest.NewRequest(http.MethodPost, "/echo", body)
	if cookieValue != "" {
		req.AddCookie(&http.Cookie{Name: e.manager.CookieName(), Value: cookieValue})
	}
	if !form {
		req.Header.Set(CSRFHeaderName, token)
	}
	return req
}

func serve(router *gin.Engine, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestCSRFRejectsPostWithoutToken is the negative acceptance case: a POST without a token returns 403.
func TestCSRFRejectsPostWithoutToken(t *testing.T) {
	e := newTestEnv(t)
	u := e.createUser(t, "judy")
	value, _ := e.startSession(t, u.ID)
	rec := doOn(csrfRouter(e), e.manager.CookieName(), http.MethodPost, "/echo", value, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST without token status = %d, want 403", rec.Code)
	}
}

func TestCSRFRejectsWrongToken(t *testing.T) {
	e := newTestEnv(t)
	u := e.createUser(t, "karl")
	value, _ := e.startSession(t, u.ID)
	rec := doOn(csrfRouter(e), e.manager.CookieName(), http.MethodPost, "/echo", value,
		map[string]string{CSRFHeaderName: "not-the-token"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST with wrong token status = %d, want 403", rec.Code)
	}
}

func TestCSRFRejectsPostWithoutSession(t *testing.T) {
	e := newTestEnv(t)
	rec := doOn(csrfRouter(e), e.manager.CookieName(), http.MethodPost, "/echo", "",
		map[string]string{CSRFHeaderName: "anything"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST without session status = %d, want 403", rec.Code)
	}
}

// TestCSRFAcceptsValidToken covers both legal carriers: the header and the form field.
func TestCSRFAcceptsValidToken(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		form    bool
	}{
		{name: "header", form: false},
		{name: "form field", form: true, headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			u := e.createUser(t, "laura")
			value, sess := e.startSession(t, u.ID)
			req := newPostRequest(t, e, value, tc.form, sess.CSRFToken)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := serve(csrfRouter(e), req)
			if rec.Code != http.StatusOK {
				t.Errorf("POST with valid CSRF token status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestCSRFAllowsGetWithoutToken(t *testing.T) {
	e := newTestEnv(t)
	u := e.createUser(t, "mallory")
	value, _ := e.startSession(t, u.ID)
	rec := doOn(csrfRouter(e), e.manager.CookieName(), http.MethodGet, "/me", value, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("GET with session status = %d, want 200", rec.Code)
	}
}
