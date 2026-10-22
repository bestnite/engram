package web

import (
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
)

// This file covers the user-visible shape of the previous fix: when a **signed-in** user clicks a link in an email (verify email, confirm email change,
// unsubscribe) or goes through forgot/reset password, the CSRF layer must let it through.
//
// Before the fix these requests necessarily got 403 csrf_failed: the contract of such endpoints is "callable anonymously too", so they only compared "double-submit cookie
// == request value", while a signed-in browser only holds a session-bound token. Now requests are tiered by "does this request have a valid session".

// TestPreSessionCSRFAcceptsSessionTokenForEveryPreSessionEndpoint covers the affected endpoints one by one:
// signed in + session-bound token must not be judged csrf_failed.
//
// The assertion targets only the CSRF layer: the handler's own decisions about email/token (e.g. an invalid token) are its own business,
// so all that is required here is "not csrf_failed".
func TestPreSessionCSRFAcceptsSessionTokenForEveryPreSessionEndpoint(t *testing.T) {
	srv, db := newAuthServer(t)
	seedAdminUser(t, srv)
	cookies, sessionCSRF := loginJSON(t, srv, db, "admin", "Sup3rSecret!")

	cases := []struct {
		path string
		body map[string]string
	}{
		{path: "/api/v1/auth/forgot-password", body: map[string]string{"email": "admin@example.com"}},
		{path: "/api/v1/auth/reset-password", body: map[string]string{"token": "irrelevant", "password": "N3wSup3rSecret!"}},
		{path: "/api/v1/auth/verify-email", body: map[string]string{"token": "irrelevant"}},
		{path: "/api/v1/auth/confirm-email-change", body: map[string]string{"token": "irrelevant"}},
		{path: "/api/v1/unsubscribe", body: map[string]string{"token": "irrelevant"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rec := postJSON(srv, tc.path, tc.body, cookies, map[string]string{auth.CSRFHeaderName: sessionCSRF})
			if rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "csrf_failed") {
				t.Fatalf("signed-in POST %s was rejected by the CSRF layer: %d %s",
					tc.path, rec.Code, snippet(rec.Body.String()))
			}
		})
	}
}

// TestPreSessionCSRFRejectsDoubleSubmitWhileSignedIn is the security side of the same fix: you cannot fall back to double-submit while signed in.
// Submitting with the pair issued by the app shell (cookie + mirror value) must still be 403.
func TestPreSessionCSRFRejectsDoubleSubmitWhileSignedIn(t *testing.T) {
	srv, db := newAuthServer(t)
	seedAdminUser(t, srv)
	cookies, _ := loginJSON(t, srv, db, "admin", "Sup3rSecret!")

	// The double-submit pair handed out by the app shell: cookie and mirror token have the same value, but it does not equal the token in the session row.
	page := get(t, srv, "/login", nil)
	doubleCookie := findCookie(page, auth.CSRFDoubleSubmitCookieName)
	if doubleCookie == nil || doubleCookie.Value == "" {
		t.Fatalf("GET /login did not issue a %s cookie", auth.CSRFDoubleSubmitCookieName)
	}
	// Send the session cookie together with the double-submit cookie: the session is still valid (the request counts as "signed in").
	jar := append(append([]*http.Cookie{}, cookies...), doubleCookie)

	rec := postJSON(srv, "/api/v1/auth/forgot-password",
		map[string]string{"email": "admin@example.com"}, jar,
		map[string]string{auth.CSRFHeaderName: doubleCookie.Value})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a double-submit pair must not pass while signed in: %d %s", rec.Code, snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "csrf_failed") {
		t.Errorf("expected the stable csrf_failed code, got %s", snippet(rec.Body.String()))
	}
}

// TestPreSessionCSRFAnonymousPathUnchanged is the regression guardrail: when signed out, double-submit is still the only channel,
// and "carrying a session token but having no session" does not hold — an anonymous request only passes with the cookie pair.
func TestPreSessionCSRFAnonymousPathUnchanged(t *testing.T) {
	srv, _ := newAuthServer(t)

	// Anonymous + cookie pair → allowed (forgot-password does not depend on token validity).
	cookie, token := doubleSubmitFromShell(t, srv)
	ok := postJSON(srv, "/api/v1/auth/forgot-password",
		map[string]string{"email": "someone@example.com"},
		[]*http.Cookie{cookie}, map[string]string{auth.CSRFHeaderName: token})
	if ok.Code != http.StatusOK {
		t.Fatalf("anonymous double-submit pair should pass: %d %s", ok.Code, snippet(ok.Body.String()))
	}

	// Anonymous + only the header (no cookie) → 403.
	bad := postJSON(srv, "/api/v1/auth/forgot-password",
		map[string]string{"email": "someone@example.com"},
		nil, map[string]string{auth.CSRFHeaderName: token})
	if bad.Code != http.StatusForbidden {
		t.Fatalf("anonymous request without the cookie must be rejected: %d %s", bad.Code, snippet(bad.Body.String()))
	}
}

// TestPreSessionCSRFEmailLinkCompletesWhileSignedIn is the end-to-end evidence for this fix: a **signed-in** user
// clicking the reset link in an email (a real token) can actually change the password, where before the fix it was blocked with 403 at the CSRF layer.
func TestPreSessionCSRFEmailLinkCompletesWhileSignedIn(t *testing.T) {
	ts := newSecurityServer(t, true) // the fixture is already signed in as owner, ts.csrf is the session-bound token

	resetToken, _ := issueResetToken(t, ts)
	rec := postJSON(ts.srv, "/api/v1/auth/reset-password",
		map[string]string{"token": resetToken, "password": "N3wSup3rSecret!"},
		ts.cookies, map[string]string{auth.CSRFHeaderName: ts.csrf})
	if rec.Code != http.StatusOK {
		t.Fatalf("signed-in reset with a real token = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// The password really did change: an anonymous login with the new password should succeed.
	cookie, headers := preSessionPair(t, ts.srv, "/login")
	login := postJSON(ts.srv, "/api/v1/auth/login",
		map[string]string{"username": "owner", "password": "N3wSup3rSecret!"},
		[]*http.Cookie{cookie}, headers)
	if login.Code != http.StatusOK {
		t.Fatalf("login with the new password = %d, want 200 (body %s)", login.Code, snippet(login.Body.String()))
	}
}
