package web

import (
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
)

// This file is the acceptance test for (pre-session double-submit CSRF).
//
// After the SSR pre-session forms were removed, all pre-session write requests are same-origin JSON endpoints (login, register, setup, forgot password,
// reset password, the login second step). They are still protected by auth.PreSessionCSRFMiddleware: the request must carry both
// the csrf_double cookie and the mirror token (the X-CSRF-Token header or the csrf_token form field).

// doubleSubmitFromShell performs one GET /login app shell and returns the csrf_double cookie and mirror token it issues.
// Under double-submit semantics the two share the same value: the token also equals the csrf_token returned by GET /api/v1/auth/session.
func doubleSubmitFromShell(t *testing.T, srv *Server) (*http.Cookie, string) {
	t.Helper()
	rec := get(t, srv, "/login", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login status = %d, want 200", rec.Code)
	}
	cookie := findCookie(rec, auth.CSRFDoubleSubmitCookieName)
	if cookie == nil || cookie.Value == "" {
		t.Fatalf("GET /login did not issue a %s cookie (body %s)", auth.CSRFDoubleSubmitCookieName, snippet(rec.Body.String()))
	}
	return cookie, cookie.Value
}

// TestPreSessionCSRFNormalBrowserFlow is the positive acceptance test: the SPA gets the cookie and mirror token from the app shell,
// then submits JSON write requests with them, and both setup and login go through.
func TestPreSessionCSRFNormalBrowserFlow(t *testing.T) {
	srv, _ := newAuthServer(t)

	cookie, token := doubleSubmitFromShell(t, srv)
	setup := postJSON(srv, "/api/v1/auth/setup", map[string]string{
		"username": "root", "email": "root@example.com", "password": "Sup3rSecret!",
	}, []*http.Cookie{cookie}, map[string]string{auth.CSRFHeaderName: token})
	if setup.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/setup with a valid double-submit pair = %d, want 200 (body %s)", setup.Code, snippet(setup.Body.String()))
	}

	loginCookie, loginToken := doubleSubmitFromShell(t, srv)
	login := postJSON(srv, "/api/v1/auth/login", map[string]string{
		"username": "root", "password": "Sup3rSecret!",
	}, []*http.Cookie{loginCookie}, map[string]string{auth.CSRFHeaderName: loginToken})
	if login.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/login with a valid double-submit pair = %d, want 200 (body %s)", login.Code, snippet(login.Body.String()))
	}
}

// TestPreSessionCSRFRejections is the negative acceptance test: pre-session JSON write requests with a missing mirror cookie or a mismatched value are always 403.
func TestPreSessionCSRFRejections(t *testing.T) {
	body := map[string]string{
		"username": "intruder", "email": "intruder@example.com", "password": "Sup3rSecret!",
	}
	cases := []struct {
		name   string
		path   string
		cookie string
		token  string
	}{
		{name: "no cookie and no token", path: "/api/v1/auth/login"},
		{name: "token without cookie", path: "/api/v1/auth/login", token: testDoubleSubmitToken},
		{name: "cookie without mirrored token", path: "/api/v1/auth/register", cookie: "a-cookie-value-0123456789ab"},
		{name: "cookie and token mismatch", path: "/api/v1/auth/register",
			cookie: "cookie-value-0123456789abc", token: "different-token-0123456789"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newAuthServer(t)
			var cookies []*http.Cookie
			if tc.cookie != "" {
				cookies = append(cookies, &http.Cookie{Name: auth.CSRFDoubleSubmitCookieName, Value: tc.cookie})
			}
			headers := map[string]string{}
			if tc.token != "" {
				headers[auth.CSRFHeaderName] = tc.token
			}
			rec := postJSON(srv, tc.path, body, cookies, headers)
			if rec.Code != http.StatusForbidden || apiErrorCode(t, rec) != "csrf_failed" {
				t.Fatalf("%s: POST %s = %d %s, want 403 csrf_failed", tc.name, tc.path, rec.Code, apiErrorCode(t, rec))
			}
		})
	}
}

// TestSessionCSRFTokenDoesNotAuthorizePreSessionForms asserts the two paths are independent:
// a session-bound token is not a double-submit cookie (it cannot authorize pre-session endpoints), and a double-submit cookie cannot authorize session endpoints either.
func TestSessionCSRFTokenDoesNotAuthorizePreSessionForms(t *testing.T) {
	srv, db := newAuthServer(t)
	seedAdminUser(t, srv)

	// Establish a session and obtain the session-bound CSRF token (server-side state).
	sessionCookies, sessionCSRF := loginJSON(t, srv, db, "admin", "Sup3rSecret!")

	// A session token without the csrf_double cookie: pre-session endpoints must reject.
	noDouble := postJSON(srv, "/api/v1/auth/register", map[string]string{
		"username": "intruder", "email": "intruder@example.com", "password": "Sup3rSecret!",
	}, sessionCookies, map[string]string{auth.CSRFHeaderName: sessionCSRF})
	if noDouble.Code != http.StatusForbidden {
		t.Fatalf("pre-session endpoint with only a session token = %d, want 403", noDouble.Code)
	}

	// A double-submit cookie cannot serve as session-bound CSRF: with a session but no session token, logout must be 403.
	doubleCookie, _ := doubleSubmitFromShell(t, srv)
	logout := postJSON(srv, "/api/v1/auth/logout", nil,
		append(sessionCookies, doubleCookie), nil)
	if logout.Code != http.StatusForbidden {
		t.Fatalf("POST /api/v1/auth/logout with only a double-submit cookie = %d, want 403", logout.Code)
	}
}
