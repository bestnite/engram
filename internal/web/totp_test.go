package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// This file is the end-to-end evidence for the three acceptances (httptest over real routes + real SQLite).
//
// The pages have moved to the SPA: both login steps and the settings reads and writes all go through same-origin JSON endpoints
// (POST /api/v1/auth/login, POST /api/v1/auth/totp, /api/v1/settings/totp*).
//  ① a user with TOTP enabled cannot complete login with the password alone;
//  ② recovery codes are single-use;
//  ③ disabling TOTP needs the password.

// createTOTPAdmin creates the first admin through the JSON bootstrap endpoint and returns its user row.
func createTOTPAdmin(t *testing.T, srv *Server, db *gorm.DB) *store.User {
	t.Helper()
	rec := setupFirstAdmin(t, srv, "admin", "admin@example.com", "Sup3rSecret!")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/setup = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var u store.User
	if err := db.First(&u, "username = ?", "admin").Error; err != nil {
		t.Fatalf("load admin: %v", err)
	}
	return &u
}

// enableTOTPFor binds and enables directly through the service layer (the JSON API flow is covered separately by totp_api_test.go),
// returning the plaintext secret and recovery codes.
func enableTOTPFor(t *testing.T, srv *Server, u *store.User) (string, []string) {
	t.Helper()
	ctx := context.Background()
	secret, otpauth, err := srv.totp.Begin(ctx, u.ID, u.Username)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if !strings.Contains(otpauth, "otpauth://totp/") {
		t.Fatalf("Begin() otpauth = %q", otpauth)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error = %v", err)
	}
	codes, err := srv.totp.Confirm(ctx, u.ID, code)
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	return secret, codes
}

// startSecondStep logs in over JSON with the correct password and returns the "waiting for second factor" cookie;
// it also asserts there is no session yet. The first factor's pending cookie is an HttpOnly value the server sets in the response.
func startSecondStep(t *testing.T, srv *Server) *http.Cookie {
	t.Helper()
	rec, _, pendingCookie := totpJSONLogin(t, srv, "admin", "Sup3rSecret!")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/login (password ok, TOTP enabled) = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "requires_totp") {
		t.Fatalf("login for a TOTP account must ask for the second factor: %s", snippet(rec.Body.String()))
	}
	if findCookie(rec, srv.sessions.CookieName()) != nil {
		t.Fatal("a session cookie was issued before the second factor")
	}
	if pendingCookie == nil {
		t.Fatal("the challenge response set no pending-second-factor cookie")
	}
	return pendingCookie
}

// TestAcceptance1_PasswordAloneCannotFinishLogin is acceptance ①.
func TestAcceptance1_PasswordAloneCannotFinishLogin(t *testing.T) {
	srv, db := newAuthServer(t)
	u := createTOTPAdmin(t, srv, db)
	secret, _ := enableTOTPFor(t, srv, u)

	// Wrong password: it must still be the generic credential error, never revealing "this account has TOTP enabled".
	bad, _, badPending := totpJSONLogin(t, srv, "admin", "WrongPassword!")
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401", bad.Code)
	}
	if badPending != nil {
		t.Error("a failed first factor issued a second-factor cookie")
	}
	if body := bad.Body.String(); strings.Contains(body, "totp") || strings.Contains(body, "otpauth") {
		t.Errorf("a failed first factor leaked TOTP state; body = %s", snippet(body))
	}

	// Correct password: the flow moves to the second step, with no session.
	pending := startSecondStep(t, srv)

	// Empty code: still no session.
	empty := postTOTPJSON(t, srv, "", nil, pending, testDoubleSubmitToken)
	if empty.Code != http.StatusUnauthorized {
		t.Fatalf("empty second factor = %d, want 401 (body %s)", empty.Code, snippet(empty.Body.String()))
	}
	if findCookie(empty, srv.sessions.CookieName()) != nil {
		t.Fatal("empty second factor issued a session cookie")
	}

	// Correct code: login completes and the session is obtained.
	// Use the next time step's code: the binding confirmation already used up the current step, so the same time step cannot pass twice.
	code, err := totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("GenerateCode() error = %v", err)
	}
	ok := postTOTPJSON(t, srv, code, nil, pending, testDoubleSubmitToken)
	if ok.Code != http.StatusOK {
		t.Fatalf("valid second factor = %d, want 200 (body %s)", ok.Code, snippet(ok.Body.String()))
	}
	if !hasSessionCookie(ok, srv) {
		t.Fatal("a valid second factor did not issue a session cookie")
	}
}

// TestAcceptance2_RecoveryCodeIsSingleUse is acceptance ② (through the JSON path).
func TestAcceptance2_RecoveryCodeIsSingleUse(t *testing.T) {
	srv, db := newAuthServer(t)
	u := createTOTPAdmin(t, srv, db)
	_, codes := enableTOTPFor(t, srv, u)

	// First use of the recovery code: login succeeds.
	first := postTOTPJSON(t, srv, codes[0], nil, startSecondStep(t, srv), testDoubleSubmitToken)
	if first.Code != http.StatusOK || !hasSessionCookie(first, srv) {
		t.Fatalf("first recovery-code login = %d (session=%v), want 200 with a session", first.Code, hasSessionCookie(first, srv))
	}

	// Second use of the same recovery code: must be refused, with no session.
	second := postTOTPJSON(t, srv, codes[0], nil, startSecondStep(t, srv), testDoubleSubmitToken)
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("reused recovery code = %d, want 401 (body %s)", second.Code, snippet(second.Body.String()))
	}
	if ck := findCookie(second, srv.sessions.CookieName()); ck != nil && ck.MaxAge >= 0 {
		t.Fatalf("reused recovery code issued a session cookie: %v", ck)
	}
}

// TestAcceptance3_DisableRequiresPassword is acceptance ③.
func TestAcceptance3_DisableRequiresPassword(t *testing.T) {
	srv, db := newAuthServer(t)
	u := createTOTPAdmin(t, srv, db)
	_, codes := enableTOTPFor(t, srv, u)

	// Complete one login through the normal flow first, to get the session cookie and the session-bound CSRF token.
	loginOK := postTOTPJSON(t, srv, codes[0], nil, startSecondStep(t, srv), testDoubleSubmitToken)
	if loginOK.Code != http.StatusOK {
		t.Fatalf("login = %d, want 200 (body %s)", loginOK.Code, snippet(loginOK.Body.String()))
	}
	cookies := loginOK.Result().Cookies()
	var sess store.Session
	if err := db.Order("created_at desc").First(&sess).Error; err != nil {
		t.Fatalf("load session row: %v", err)
	}

	// Wrong password: the disable is refused, TOTP stays enabled.
	wrong := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/disable", map[string]any{"password": "WrongPassword!"}, cookies, sess.CSRFToken)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("disable with a wrong password = %d, want 401 (body %s)", wrong.Code, snippet(wrong.Body.String()))
	}
	if enabled, _ := srv.totp.Enabled(context.Background(), u.ID); !enabled {
		t.Fatal("TOTP was disabled despite a wrong password")
	}

	// Correct password: the disable succeeds.
	right := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/disable", map[string]any{"password": "Sup3rSecret!"}, cookies, sess.CSRFToken)
	if right.Code != http.StatusOK {
		t.Fatalf("disable with the correct password = %d, want 200 (body %s)", right.Code, snippet(right.Body.String()))
	}
	if enabled, _ := srv.totp.Enabled(context.Background(), u.ID); enabled {
		t.Fatal("TOTP is still enabled after a confirmed disable")
	}
}

// hasSessionCookie reports whether the response set a valid session cookie.
func hasSessionCookie(rec *httptest.ResponseRecorder, srv *Server) bool {
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == srv.sessions.CookieName() && ck.Value != "" && ck.MaxAge >= 0 {
			return true
		}
	}
	return false
}
