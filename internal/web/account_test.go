package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the acceptance test for the SPA transport layer of account security and mail flows (internal/web/account.go).
//
// Covers: the shell cutover of the navigable read pages, token consumption for the sign-in-free one-click links (app shell + JSON endpoint), the one-shot/expiry
// semantics of the same-origin JSON endpoints' tokens, anonymous rate limiting, session and CSRF negative cases, and that the mailbox is not changed before confirmation. Everything runs through the real routes and SQLite.

// postJSONWithSession sends one JSON write request with the session cookie + session CSRF header.
func postJSONWithSession(srv *Server, path string, body any, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
	return postJSON(srv, path, body, cookies, map[string]string{auth.CSRFHeaderName: csrf})
}

// decodeJSON decodes the response body into out; Fatals on failure.
func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decode JSON response failed: %v (body %s)", err, snippet(rec.Body.String()))
	}
}

// jsonResetToken triggers a reset mail via the JSON POST /api/v1/auth/forgot-password and extracts the plaintext token.
func jsonResetToken(t *testing.T, ts securityTestServer, cookie *http.Cookie, headers map[string]string) string {
	t.Helper()
	before := len(outboxByType(t, ts.db, mail.TypePasswordReset))
	rec := postJSON(ts.srv, "/api/v1/auth/forgot-password",
		map[string]string{"email": "owner@example.com"}, []*http.Cookie{cookie}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/forgot-password status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	rows := outboxByType(t, ts.db, mail.TypePasswordReset)
	if len(rows) != before+1 {
		t.Fatalf("password_reset rows %d -> %d after a JSON reset request, want +1", before, len(rows))
	}
	return tokenFromBody(t, rows[len(rows)-1].TextBody)
}

// TestAccountRouteCutover covers the app shell of the three navigable read pages and the two one-click links, the sign-in gate on /settings/email,
// and that the one-click link entry points initialize the double-submit cookie without consuming any token.
func TestAccountRouteCutover(t *testing.T) {
	ts := newSecurityServer(t, true)

	for _, path := range []string{"/forgot-password", "/reset-password", "/settings/email"} {
		assertShell(t, getWithCookies(t, ts.srv, path, ts.cookies))
	}

	// The one-click link in emails lands on the canonical path and likewise returns the app shell (the server no longer renders a result page).
	for _, path := range []string{"/verify-email?token=bogus", "/confirm-email-change?token=bogus"} {
		rec := get(t, ts.srv, path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200 (body %s)", path, rec.Code, snippet(rec.Body.String()))
		}
		if !strings.Contains(rec.Body.String(), `id="app"`) {
			t.Errorf("GET %s did not serve the SPA shell: %s", path, snippet(rec.Body.String()))
		}
		if findCookie(rec, auth.CSRFDoubleSubmitCookieName) == nil {
			t.Errorf("GET %s must initialize the double-submit cookie", path)
		}
	}

	// /settings/email when signed out still redirects to the login page: authorization is decided server-side, before the shell cutover.
	anon := get(t, ts.srv, "/settings/email", nil)
	if anon.Code != http.StatusSeeOther || anon.Header().Get("Location") != "/login" {
		t.Errorf("anonymous GET /settings/email = %d %q, want 303 /login", anon.Code, anon.Header().Get("Location"))
	}
}

// TestAccountJSON_CSRFRejections asserts that pre-session JSON endpoints reuse double-submit CSRF: a missing cookie / missing mirror
// value is always 403 csrf_failed and triggers no business action.
func TestAccountJSON_CSRFRejections(t *testing.T) {
	ts := newSecurityServer(t, true)
	for _, path := range []string{
		"/api/v1/auth/forgot-password",
		"/api/v1/auth/reset-password",
		"/api/v1/auth/verify-email",
		"/api/v1/auth/confirm-email-change",
	} {
		rec := postJSON(ts.srv, path, map[string]string{"token": "x"}, nil, nil)
		if rec.Code != http.StatusForbidden || apiErrorCode(t, rec) != "csrf_failed" {
			t.Errorf("POST %s without CSRF = %d %s, want 403 csrf_failed", path, rec.Code, apiErrorCode(t, rec))
		}
	}
	if rows := outboxByType(t, ts.db, mail.TypePasswordReset); len(rows) != 0 {
		t.Errorf("CSRF-rejected reset requests enqueued %d mails, want 0", len(rows))
	}
}

// TestAccountForgotResetFlow covers the JSON reset request and submission: a successful password change, token one-shotness, expiry and
// rejection of weak passwords, and that the new password really can sign in.
func TestAccountForgotResetFlow(t *testing.T) {
	ts := newSecurityServer(t, true)
	cookie, headers := preSessionPair(t, ts.srv, "/forgot-password")

	// Request reset: the response contains only the site-level mail_ready, and a reset mail really was enqueued.
	rec := postJSON(ts.srv, "/api/v1/auth/forgot-password",
		map[string]string{"email": "owner@example.com"}, []*http.Cookie{cookie}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("forgot-password status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var forgot struct {
		MailReady bool `json:"mail_ready"`
	}
	decodeJSON(t, rec, &forgot)
	if !forgot.MailReady {
		t.Errorf("forgot-password mail_ready = false, want true when SMTP is configured")
	}
	plain := jsonResetToken(t, ts, cookie, headers)

	// Submit new password: succeeds and the new password can sign in.
	reset := postJSON(ts.srv, "/api/v1/auth/reset-password",
		map[string]string{"token": plain, "password": "N3wSup3rSecret!"}, []*http.Cookie{cookie}, headers)
	if reset.Code != http.StatusOK {
		t.Fatalf("reset-password status = %d, want 200 (body %s)", reset.Code, snippet(reset.Body.String()))
	}
	var resetRes struct {
		Reset bool `json:"reset"`
	}
	decodeJSON(t, reset, &resetRes)
	if !resetRes.Reset {
		t.Errorf("reset-password reset = false, want true")
	}
	// The new password really can sign in (via the same-origin JSON login endpoint).
	loginJSON(t, ts.srv, ts.db, "owner", "N3wSup3rSecret!")

	// Reusing the same token: token_used.
	reuse := postJSON(ts.srv, "/api/v1/auth/reset-password",
		map[string]string{"token": plain, "password": "An0therSecret!"}, []*http.Cookie{cookie}, headers)
	if reuse.Code != http.StatusBadRequest || apiErrorCode(t, reuse) != "token_used" {
		t.Errorf("reused reset token = %d %s, want 400 token_used", reuse.Code, apiErrorCode(t, reuse))
	}

	// Weak password: stops at the password policy code (the token has already been consumed, same as SSR, so a fresh token is fetched here first).
	weak := jsonResetToken(t, ts, cookie, headers)
	weakRec := postJSON(ts.srv, "/api/v1/auth/reset-password",
		map[string]string{"token": weak, "password": "short"}, []*http.Cookie{cookie}, headers)
	if weakRec.Code != http.StatusBadRequest || apiErrorCode(t, weakRec) != "password_too_short" {
		t.Errorf("weak password = %d %s, want 400 password_too_short", weakRec.Code, apiErrorCode(t, weakRec))
	}

	// Expired token: token_expired.
	expired, err := ts.srv.tokens.Issue(context.Background(), ts.ownerID, store.ActionTokenPasswordReset, "", -time.Minute)
	if err != nil {
		t.Fatalf("issue expired reset token: %v", err)
	}
	expiredRec := postJSON(ts.srv, "/api/v1/auth/reset-password",
		map[string]string{"token": expired, "password": "N3wSup3rSecret!"}, []*http.Cookie{cookie}, headers)
	if expiredRec.Code != http.StatusBadRequest || apiErrorCode(t, expiredRec) != "token_expired" {
		t.Errorf("expired token = %d %s, want 400 token_expired", expiredRec.Code, apiErrorCode(t, expiredRec))
	}
}

// TestAccountEmailVerifyFlows covers reading/resending/changing the email on the session endpoints, plus the sign-in-free verify and confirm JSON endpoints.
func TestAccountEmailVerifyFlows(t *testing.T) {
	ts := newSecurityServer(t, true)

	// GET /api/v1/settings/email: current mailbox + unverified + mail available.
	rec := getWithCookies(t, ts.srv, "/api/v1/settings/email", ts.cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/settings/email status = %d, want 200", rec.Code)
	}
	var settings struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		MailReady     bool   `json:"mail_ready"`
	}
	decodeJSON(t, rec, &settings)
	if settings.Email != "owner@example.com" || settings.EmailVerified || !settings.MailReady {
		t.Errorf("email settings = %+v, want owner@example.com / unverified / mail_ready", settings)
	}

	// Anonymous read of a session endpoint: 401.
	if anon := get(t, ts.srv, "/api/v1/settings/email", nil); anon.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET /api/v1/settings/email = %d, want 401", anon.Code)
	}

	// Resend the verification mail (session CSRF).
	resend := postJSONWithSession(ts.srv, "/api/v1/settings/verify-email", nil, ts.cookies, ts.csrf)
	if resend.Code != http.StatusOK {
		t.Fatalf("resend verification status = %d, want 200 (body %s)", resend.Code, snippet(resend.Body.String()))
	}
	verifyRows := outboxByType(t, ts.db, mail.TypeEmailVerification)
	if len(verifyRows) == 0 {
		t.Fatal("resend verification enqueued no email_verification mail")
	}
	verifyToken := tokenFromBody(t, verifyRows[len(verifyRows)-1].TextBody)

	// Consume the verification token (sign-in-free, double-submit CSRF).
	vc, vh := preSessionPair(t, ts.srv, "/verify-email")
	verified := postJSON(ts.srv, "/api/v1/auth/verify-email",
		map[string]string{"token": verifyToken}, []*http.Cookie{vc}, vh)
	if verified.Code != http.StatusOK {
		t.Fatalf("verify-email status = %d, want 200 (body %s)", verified.Code, snippet(verified.Body.String()))
	}
	var owner store.User
	if err := ts.db.First(&owner, ts.ownerID).Error; err != nil {
		t.Fatalf("reload owner: %v", err)
	}
	if owner.EmailVerifiedAt == nil {
		t.Error("email_verified_at is still NULL after JSON verification")
	}
	// Second use: token_used.
	again := postJSON(ts.srv, "/api/v1/auth/verify-email",
		map[string]string{"token": verifyToken}, []*http.Cookie{vc}, vh)
	if again.Code != http.StatusBadRequest || apiErrorCode(t, again) != "token_used" {
		t.Errorf("reused verification token = %d %s, want 400 token_used", again.Code, apiErrorCode(t, again))
	}

	// Email-change request: no session CSRF → 403; malformed format → email_invalid; same as the original mailbox → email_same.
	if noCSRF := postJSON(ts.srv, "/api/v1/settings/email", map[string]string{"email": "x@example.com"}, ts.cookies, nil); noCSRF.Code != http.StatusForbidden {
		t.Errorf("settings/email without session CSRF = %d, want 403", noCSRF.Code)
	}
	invalid := postJSONWithSession(ts.srv, "/api/v1/settings/email", map[string]string{"email": "not-an-email"}, ts.cookies, ts.csrf)
	if invalid.Code != http.StatusBadRequest || apiErrorCode(t, invalid) != "email_invalid" {
		t.Errorf("invalid new email = %d %s, want 400 email_invalid", invalid.Code, apiErrorCode(t, invalid))
	}
	same := postJSONWithSession(ts.srv, "/api/v1/settings/email", map[string]string{"email": "owner@example.com"}, ts.cookies, ts.csrf)
	if same.Code != http.StatusBadRequest || apiErrorCode(t, same) != "email_same" {
		t.Errorf("same new email = %d %s, want 400 email_same", same.Code, apiErrorCode(t, same))
	}
	// Already taken → email_taken.
	taken := &store.User{
		Username: "takenuser", Email: "taken@example.com", DisplayName: "Taken",
		Role: store.RoleUser, Status: store.StatusActive, Locale: "en",
		Timezone: "UTC", CreatedAt: time.Now().UTC(),
	}
	if err := store.NewUserStore(ts.db).Create(context.Background(), taken); err != nil {
		t.Fatalf("create taken user: %v", err)
	}
	takenRec := postJSONWithSession(ts.srv, "/api/v1/settings/email", map[string]string{"email": "taken@example.com"}, ts.cookies, ts.csrf)
	if takenRec.Code != http.StatusBadRequest || apiErrorCode(t, takenRec) != "email_taken" {
		t.Errorf("taken new email = %d %s, want 400 email_taken", takenRec.Code, apiErrorCode(t, takenRec))
	}

	// Successful email-change request: enqueued to the new address, and the DB stays unchanged before confirmation.
	before := len(outboxByType(t, ts.db, mail.TypeEmailVerification))
	change := postJSONWithSession(ts.srv, "/api/v1/settings/email", map[string]string{"email": "new-owner@example.com"}, ts.cookies, ts.csrf)
	if change.Code != http.StatusOK {
		t.Fatalf("email change request status = %d, want 200 (body %s)", change.Code, snippet(change.Body.String()))
	}
	rows := outboxByType(t, ts.db, mail.TypeEmailVerification)
	if len(rows) != before+1 || rows[len(rows)-1].To != "new-owner@example.com" {
		t.Fatalf("email change confirmation not enqueued to the new address: %+v", rows)
	}
	if err := ts.db.First(&owner, ts.ownerID).Error; err != nil {
		t.Fatalf("reload owner: %v", err)
	}
	if owner.Email != "owner@example.com" {
		t.Fatalf("email changed before confirmation: %q", owner.Email)
	}

	// Confirm the email change (sign-in-free, double-submit CSRF): the DB is actually updated.
	changeToken := tokenFromBody(t, rows[len(rows)-1].TextBody)
	cc, ch := preSessionPair(t, ts.srv, "/confirm-email-change")
	confirm := postJSON(ts.srv, "/api/v1/auth/confirm-email-change",
		map[string]string{"token": changeToken}, []*http.Cookie{cc}, ch)
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm-email-change status = %d, want 200 (body %s)", confirm.Code, snippet(confirm.Body.String()))
	}
	if err := ts.db.First(&owner, ts.ownerID).Error; err != nil {
		t.Fatalf("reload owner: %v", err)
	}
	if owner.Email != "new-owner@example.com" {
		t.Errorf("email after confirmation = %q, want new-owner@example.com", owner.Email)
	}
}

// TestShellEndpointsServeAppAndDoubleSubmitCookie asserts that /verify-email and
// /confirm-email-change issue the double-submit cookie, return the app shell, and consume no token.
func TestShellEndpointsServeAppAndDoubleSubmitCookie(t *testing.T) {
	ts := newSecurityServer(t, true)
	for _, path := range []string{"/verify-email", "/confirm-email-change"} {
		rec := get(t, ts.srv, path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `id="app"`) {
			t.Errorf("GET %s did not serve the SPA shell: %s", path, snippet(rec.Body.String()))
		}
		if findCookie(rec, auth.CSRFDoubleSubmitCookieName) == nil {
			t.Errorf("GET %s must initialize the double-submit cookie", path)
		}
	}
}
