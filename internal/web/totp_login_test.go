package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the end-to-end evidence for the SPA login second step (internal/web/totp_login.go):
// real routes + real SQLite, reusing auth.TOTPService, with asserted states matching the SSR form path.
//
// Coverage: the state semantics of the pending query, double-submit CSRF enforcement, missing/expired credentials, a wrong code (audit included),
// recovery-code consumption, the session and session cookie issued on success, and the /login/totp shell.
//
// Time-step caveat: Confirm records "the largest accepted time step" (same rule as login), so after enabling you must log in with the
// next step's code — the fixture consistently uses time.Now().Add(totpPeriod*time.Second) (as the existing TOTP tests do).

// totpJSONLogin goes through the SPA's JSON login (POST /api/v1/auth/login) and returns the response plus the cookies before and after.
// When the first factor passes but the account has TOTP, the server issues the second-step credential cookie and returns requires_totp=true.
func totpJSONLogin(t *testing.T, srv *Server, username, password string) (*httptest.ResponseRecorder, *http.Cookie, *http.Cookie) {
	t.Helper()
	initRec := get(t, srv, "/api/v1/auth/session", nil)
	doubleCookie := findCookie(initRec, auth.CSRFDoubleSubmitCookieName)
	if doubleCookie == nil {
		t.Fatal("GET /api/v1/auth/session did not set the double-submit cookie")
	}
	var initRes struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(initRec.Body.Bytes(), &initRes); err != nil {
		t.Fatalf("unmarshal session init: %v", err)
	}
	rec := postJSON(srv, "/api/v1/auth/login", map[string]string{
		"username": username, "password": password,
	}, []*http.Cookie{doubleCookie}, map[string]string{auth.CSRFHeaderName: initRes.CSRFToken})
	return rec, doubleCookie, findCookie(rec, totpPendingCookieName)
}

// postTOTPJSON submits the login second step. Double-submit CSRF requires the cookie and the mirrored header to match: with a non-empty csrf both
// are derived from it; with an empty csrf the request goes out as-is (to cover the missing-token rejection path).
func postTOTPJSON(t *testing.T, srv *Server, code string, cookies []*http.Cookie, csrfCookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	headers := map[string]string{}
	all := append([]*http.Cookie{}, cookies...)
	if csrf != "" {
		headers[auth.CSRFHeaderName] = csrf
		all = append(all, &http.Cookie{Name: auth.CSRFDoubleSubmitCookieName, Value: csrf})
	}
	if csrfCookie != nil {
		all = append(all, csrfCookie)
	}
	return postJSON(srv, "/api/v1/auth/totp", map[string]string{"code": code}, all, headers)
}

// TestTOTPLoginPendingReflectsChallengeState asserts pending reflects only "whether a valid second-step credential is held",
// and an anonymous request always gets false (leaking nothing about whether any account has TOTP).
func TestTOTPLoginPendingReflectsChallengeState(t *testing.T) {
	srv, db := newAuthServer(t)
	_ = createTOTPAdmin(t, srv, db)
	cookies, csrf := totpLogin(t, srv, db)
	_ = totpEnable(t, srv, cookies, csrf)

	// Anonymous: no credential, and no TOTP material appears in the body.
	anon := getWithCookies(t, srv, "/api/v1/auth/totp", nil)
	if anon.Code != http.StatusOK {
		t.Fatalf("anonymous GET pending = %d, want 200 (body %s)", anon.Code, snippet(anon.Body.String()))
	}
	assertNoTOTPSecret(t, anon.Body.String())
	var anonRes struct {
		Pending bool `json:"pending"`
	}
	if err := json.Unmarshal(anon.Body.Bytes(), &anonRes); err != nil {
		t.Fatal(err)
	}
	if anonRes.Pending {
		t.Fatalf("anonymous pending = true, want false")
	}

	// The credential is only issued after the first factor passes: from then on pending=true.
	loginRec, _, pendingCookie := totpJSONLogin(t, srv, "admin", "Sup3rSecret!")
	if loginRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/login = %d, want 200 (body %s)", loginRec.Code, snippet(loginRec.Body.String()))
	}
	if !strings.Contains(loginRec.Body.String(), "requires_totp") {
		t.Fatalf("login for a TOTP account must ask for the second factor: %s", snippet(loginRec.Body.String()))
	}
	if pendingCookie == nil {
		t.Fatal("login did not set the second-factor pending cookie")
	}
	if findCookie(loginRec, srv.sessions.CookieName()) != nil {
		t.Error("login must not issue a session cookie before the second factor")
	}

	withChallenge := getWithCookies(t, srv, "/api/v1/auth/totp", []*http.Cookie{pendingCookie})
	var withRes struct {
		Pending bool `json:"pending"`
	}
	if err := json.Unmarshal(withChallenge.Body.Bytes(), &withRes); err != nil {
		t.Fatal(err)
	}
	if !withRes.Pending {
		t.Fatalf("pending with a valid challenge cookie = false, want true")
	}
	assertNoTOTPSecret(t, withChallenge.Body.String())
}

// TestTOTPLoginSecondStepIssuesSession covers: a wrong code is refused and traced, a correct code issues a session,
// the credential is cleaned up, and the session cookie is usable at once.
func TestTOTPLoginSecondStepIssuesSession(t *testing.T) {
	srv, db := newAuthServer(t)
	_ = createTOTPAdmin(t, srv, db)
	cookies, csrf := totpLogin(t, srv, db)
	secret := totpEnable(t, srv, cookies, csrf)

	_, _, pendingCookie := totpJSONLogin(t, srv, "admin", "Sup3rSecret!")
	if pendingCookie == nil {
		t.Fatal("login did not set the second-factor pending cookie")
	}

	// Wrong code: 401 + a stable code, and one totp.verify_failed audit entry is written.
	bad := postTOTPJSON(t, srv, "000000", nil, pendingCookie, testDoubleSubmitToken)
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("POST with a wrong code = %d, want 401 (body %s)", bad.Code, snippet(bad.Body.String()))
	}
	if !strings.Contains(bad.Body.String(), "totp_invalid") {
		t.Errorf("wrong code must report totp_invalid, got %s", snippet(bad.Body.String()))
	}
	if findCookie(bad, srv.sessions.CookieName()) != nil {
		t.Error("a rejected second factor must not issue a session cookie")
	}
	audits := store.NewAuditStore(db)
	if n, err := audits.CountByAction(context.Background(), store.ActionTOTPVerifyFailed); err != nil || n != 1 {
		t.Fatalf("totp.verify_failed audit = (%d, %v), want 1", n, err)
	}

	// Correct code: it must be the next step's code (Confirm already consumed the current time step).
	code, err := totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("GenerateCode() error = %v", err)
	}
	ok := postTOTPJSON(t, srv, code, nil, pendingCookie, testDoubleSubmitToken)
	if ok.Code != http.StatusOK {
		t.Fatalf("POST with a valid code = %d, want 200 (body %s)", ok.Code, snippet(ok.Body.String()))
	}
	var okRes struct {
		Authenticated bool `json:"authenticated"`
		User          struct {
			Username    string `json:"username"`
			HasPassword bool   `json:"has_password"`
		} `json:"user"`
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(ok.Body.Bytes(), &okRes); err != nil {
		t.Fatal(err)
	}
	if !okRes.Authenticated || okRes.User.Username != "admin" || okRes.CSRFToken == "" {
		t.Fatalf("second-step success response = %+v, want an authenticated admin with a CSRF token", okRes)
	}
	if !okRes.User.HasPassword {
		t.Error("second-step success response for a password account has has_password=false")
	}
	sessCookie := findCookie(ok, srv.sessions.CookieName())
	if sessCookie == nil {
		t.Fatal("a passing second factor must issue a session cookie")
	}

	// The session is usable at once.
	me := getWithCookies(t, srv, "/api/v1/auth/session", []*http.Cookie{sessCookie})
	var meRes struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &meRes); err != nil {
		t.Fatal(err)
	}
	if !meRes.Authenticated {
		t.Errorf("session after the second factor is not authenticated: %s", snippet(me.Body.String()))
	}

	// The second-step credential is cleaned up: the success response blanks and expires that cookie (so the browser drops it).
	cleared := findCookie(ok, totpPendingCookieName)
	if cleared == nil || cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Errorf("successful login must clear %s, got %+v", totpPendingCookieName, cleared)
	}

	// The successful-login audit carries second_factor=totp (same criterion as SSR).
	if n, err := audits.CountByAction(context.Background(), store.ActionUserLoginSucceeded); err != nil || n != 2 {
		t.Fatalf("user.login_succeeded audit = (%d, %v), want 2 (initial setup login + second step)", n, err)
	}
}

// TestTOTPLoginSecondStepGuards covers two rejection paths: a missing double-submit token, and no second-step credential.
func TestTOTPLoginSecondStepGuards(t *testing.T) {
	srv, db := newAuthServer(t)
	_ = createTOTPAdmin(t, srv, db)
	cookies, csrf := totpLogin(t, srv, db)
	_ = totpEnable(t, srv, cookies, csrf)

	_, _, pendingCookie := totpJSONLogin(t, srv, "admin", "Sup3rSecret!")
	if pendingCookie == nil {
		t.Fatal("login did not set the second-factor pending cookie")
	}

	// Missing CSRF: the double-submit middleware must intercept, and no session may be issued.
	noCSRF := postTOTPJSON(t, srv, "123456", nil, nil, "")
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("POST without the double-submit token = %d, want 403 (body %s)", noCSRF.Code, snippet(noCSRF.Body.String()))
	}
	if findCookie(noCSRF, srv.sessions.CookieName()) != nil {
		t.Error("a CSRF-rejected request must not issue a session cookie")
	}

	// No second-step credential: a stable code asks for a new login, and no session is issued.
	noChallenge := postTOTPJSON(t, srv, "123456", nil, nil, testDoubleSubmitToken)
	if noChallenge.Code != http.StatusUnauthorized {
		t.Fatalf("POST without a pending challenge = %d, want 401 (body %s)", noChallenge.Code, snippet(noChallenge.Body.String()))
	}
	if !strings.Contains(noChallenge.Body.String(), "totp_challenge_expired") {
		t.Errorf("missing challenge must report totp_challenge_expired, got %s", snippet(noChallenge.Body.String()))
	}
	if findCookie(noChallenge, srv.sessions.CookieName()) != nil {
		t.Error("a request without a challenge must not issue a session cookie")
	}
}

// TestTOTPLoginShellServesAppShell asserts the login second step has no dedicated GET page route: GET /login/totp
// falls through to the NoRoute fallback and returns the app shell (and therefore sets no double-submit cookie — the anonymous CSRF one comes from
// GET /api/v1/auth/session), and POST /login/totp is not a registered route (anything but GET is 404).
func TestTOTPLoginShellServesAppShell(t *testing.T) {
	srv, _ := newAuthServer(t)
	rec := get(t, srv, "/login/totp", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login/totp = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET /login/totp Content-Type = %q, want text/html", ct)
	}
	if findCookie(rec, srv.sessions.CookieName()) != nil {
		t.Error("GET /login/totp must not issue a session cookie")
	}
	// The SSR second-step form endpoint was removed with the page layer: POST /login/totp is no longer a registered route.
	ssrStep := postJSON(srv, "/login/totp", map[string]string{"code": "123456"}, nil, nil)
	if ssrStep.Code != http.StatusNotFound {
		t.Errorf("POST /login/totp = %d, want 404 after the SSR endpoint was removed", ssrStep.Code)
	}
}
