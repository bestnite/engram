package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// This file covers SPA registration and first-admin bootstrap: the GET /register and GET /setup app shells,
// plus the two JSON endpoints POST /api/v1/auth/register and POST /api/v1/auth/setup.
// Security semantics match the SSR forms: pre-session double-submit CSRF, registration policy/email allowlist/invite transaction, the one-time
// bootstrap admin gate, and no session established on success. Everything runs through the real routes and SQLite, with no mocked database.

// apiErrorCode extracts the stable English code from a JSON error envelope.
func apiErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal error envelope: %v (body %s)", err, snippet(rec.Body.String()))
	}
	return env.Error.Code
}

// preSessionPair performs one GET to obtain the pre-session double-submit cookie and the mirror token, for use by JSON write requests.
func preSessionPair(t *testing.T, srv *Server, path string) (*http.Cookie, map[string]string) {
	t.Helper()
	rec := get(t, srv, path, nil)
	cookie := findCookie(rec, auth.CSRFDoubleSubmitCookieName)
	if cookie == nil {
		t.Fatalf("GET %s did not set %s", path, auth.CSRFDoubleSubmitCookieName)
	}
	return cookie, map[string]string{auth.CSRFHeaderName: cookie.Value}
}

// countUsersByEmail counts the accounts with the given email, to assert whether account creation really happened.
func countUsersByEmail(t *testing.T, srv *Server, email string) int64 {
	t.Helper()
	var n int64
	if err := srv.db.Model(&store.User{}).Where("email = ?", email).Count(&n).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	return n
}

// TestRegisterShellServesAppAndInitializesDoubleSubmitCookie asserts GET /register
// returns the app shell and issues the pre-session double-submit cookie, but establishes no session and renders no SSR form.
func TestRegisterShellServesAppAndInitializesDoubleSubmitCookie(t *testing.T) {
	srv, _ := newAuthServer(t)

	rec := get(t, srv, "/register", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /register status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET /register Content-Type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<div id="app"></div>`) {
		t.Errorf("GET /register did not serve the SPA shell: %s", snippet(body))
	}
	if strings.Contains(body, `name="password"`) || strings.Contains(body, `action="/register"`) {
		t.Errorf("GET /register must not render the SSR register form: %s", snippet(body))
	}
	c := findCookie(rec, auth.CSRFDoubleSubmitCookieName)
	if c == nil {
		t.Fatalf("GET /register did not set %s", auth.CSRFDoubleSubmitCookieName)
	}
	if !c.HttpOnly {
		t.Errorf("csrf_double cookie must be HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("csrf_double cookie SameSite = %v, want Lax", c.SameSite)
	}
	if findCookie(rec, srv.sessions.CookieName()) != nil {
		t.Errorf("GET /register must not issue a session cookie")
	}
}

// TestSetupShellAvailability asserts GET /setup returns the app shell only when there is no active admin,
// and returns 404 when an admin already exists (consistent with SSR's one-time admin gate).
func TestSetupShellAvailability(t *testing.T) {
	// First-start window: no admin, reachable, double-submit cookie issued.
	fresh, _ := newAuthServer(t)
	rec := get(t, fresh, "/setup", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /setup (no admin) status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `<div id="app"></div>`) {
		t.Errorf("GET /setup did not serve the SPA shell: %s", snippet(rec.Body.String()))
	}
	if findCookie(rec, auth.CSRFDoubleSubmitCookieName) == nil {
		t.Errorf("GET /setup must initialize the double-submit cookie")
	}
	if findCookie(rec, fresh.sessions.CookieName()) != nil {
		t.Errorf("GET /setup must not issue a session cookie")
	}

	// An admin already exists: the bootstrap window is closed, must be 404.
	seeded, _ := newAuthServer(t)
	seedAdminUser(t, seeded)
	closed := get(t, seeded, "/setup", nil)
	if closed.Code != http.StatusNotFound {
		t.Fatalf("GET /setup (admin exists) status = %d, want 404", closed.Code)
	}
}

// TestRegisterAPI_CSRFRejections asserts the registration JSON endpoint reuses the pre-session double-submit middleware:
// a missing cookie, a missing mirror token or a mismatched value is always 403 csrf_failed, and no account is created.
func TestRegisterAPI_CSRFRejections(t *testing.T) {
	srv, _ := newAuthServer(t)
	seedAdminUser(t, srv)
	writeSetting(t, srv.db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, "open")))

	body := map[string]string{
		"username": "intruder", "email": "intruder@example.com", "password": "Sup3rSecret!",
	}

	// 1. No cookie and no mirror token.
	none := postJSON(srv, "/api/v1/auth/register", body, nil, nil)
	if none.Code != http.StatusForbidden || apiErrorCode(t, none) != "csrf_failed" {
		t.Errorf("missing CSRF = %d %s, want 403 csrf_failed", none.Code, apiErrorCode(t, none))
	}

	// 2. Cookie present but no mirror token.
	cookie := &http.Cookie{Name: auth.CSRFDoubleSubmitCookieName, Value: testDoubleSubmitToken}
	noHeader := postJSON(srv, "/api/v1/auth/register", body, []*http.Cookie{cookie}, nil)
	if noHeader.Code != http.StatusForbidden || apiErrorCode(t, noHeader) != "csrf_failed" {
		t.Errorf("cookie without header = %d %s, want 403 csrf_failed", noHeader.Code, apiErrorCode(t, noHeader))
	}

	// 3. Cookie present but the mirror value does not match.
	mismatch := postJSON(srv, "/api/v1/auth/register", body, []*http.Cookie{cookie},
		map[string]string{auth.CSRFHeaderName: "mismatched-token-value-123456789"})
	if mismatch.Code != http.StatusForbidden || apiErrorCode(t, mismatch) != "csrf_failed" {
		t.Errorf("mismatched CSRF = %d %s, want 403 csrf_failed", mismatch.Code, apiErrorCode(t, mismatch))
	}

	if n := countUsersByEmail(t, srv, "intruder@example.com"); n != 0 {
		t.Errorf("CSRF-rejected registrations created %d users, want 0", n)
	}
}

// TestRegisterAPI_PolicyMatrix asserts the JSON registration endpoint makes the same policy decisions as SSR:
// open allows, closed rejects, invite without a token rejects, allowlist mismatch rejects.
func TestRegisterAPI_PolicyMatrix(t *testing.T) {
	cases := []struct {
		name       string
		policy     string
		allowlist  string
		email      string
		wantStatus int
		wantCode   string
	}{
		{name: "open allows anyone", policy: "open", email: "alice@example.org", wantStatus: http.StatusOK},
		{name: "open allowlist denial", policy: "open", allowlist: `["example.com"]`,
			email: "alice@example.net", wantStatus: http.StatusForbidden, wantCode: "email_domain_not_allowed"},
		{name: "invite requires a token", policy: "invite",
			email: "alice@example.com", wantStatus: http.StatusForbidden, wantCode: "invite_required"},
		{name: "closed rejects everyone", policy: "closed",
			email: "alice@example.com", wantStatus: http.StatusForbidden, wantCode: "registration_closed"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := newAuthServer(t)
			seedAdminUser(t, srv)
			writeSetting(t, db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, tc.policy)))
			if tc.allowlist != "" {
				writeSetting(t, db, auth.SettingKeyEmailAllowlist, tc.allowlist)
			}
			cookie, headers := preSessionPair(t, srv, "/register")
			rec := postJSON(srv, "/api/v1/auth/register", map[string]string{
				"username": string(rune('a'+i)) + "_user",
				"email":    tc.email,
				"password": "Sup3rSecret!",
			}, []*http.Cookie{cookie}, headers)

			if rec.Code != tc.wantStatus {
				t.Fatalf("POST /api/v1/auth/register (%s) status = %d, want %d (body %s)",
					tc.name, rec.Code, tc.wantStatus, snippet(rec.Body.String()))
			}
			if tc.wantCode != "" && apiErrorCode(t, rec) != tc.wantCode {
				t.Errorf("code = %q, want %q", apiErrorCode(t, rec), tc.wantCode)
			}
			wantUsers := int64(0)
			if tc.wantStatus == http.StatusOK {
				wantUsers = 1
			}
			if n := countUsersByEmail(t, srv, tc.email); n != wantUsers {
				t.Errorf("users for %s = %d, want %d", tc.email, n, wantUsers)
			}
		})
	}
}

// TestRegisterAPI_SuccessIssuesNoSession asserts a successful registration only returns created=true
// and issues no session cookie (consistent with SSR redirecting to login after registration).
func TestRegisterAPI_SuccessIssuesNoSession(t *testing.T) {
	srv, db := newAuthServer(t)
	seedAdminUser(t, srv)
	writeSetting(t, db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, "open")))

	cookie, headers := preSessionPair(t, srv, "/register")
	rec := postJSON(srv, "/api/v1/auth/register", map[string]string{
		"username": "newcomer", "email": "newcomer@example.com", "password": "Sup3rSecret!",
	}, []*http.Cookie{cookie}, headers)

	if rec.Code != http.StatusOK {
		t.Fatalf("register status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var res struct {
		Created bool `json:"created"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal register response: %v", err)
	}
	if !res.Created {
		t.Errorf("expected created=true, got %s", snippet(rec.Body.String()))
	}
	if findCookie(rec, srv.sessions.CookieName()) != nil {
		t.Errorf("registration must not issue a session cookie")
	}
	if n := countUsersByEmail(t, srv, "newcomer@example.com"); n != 1 {
		t.Errorf("users created = %d, want 1", n)
	}
}

// TestRegisterAPI_Invite asserts the invite path matches SSR: a valid invite creates the account once and claims the token,
// reusing the same token returns 403 invite_invalid and produces no second user.
func TestRegisterAPI_Invite(t *testing.T) {
	srv, db := newAuthServer(t)
	seedAdminUser(t, srv)
	writeSetting(t, db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, "invite")))
	invites := store.NewInviteStore(db)
	inv := &store.Invite{}
	if err := invites.Create(context.Background(), inv); err != nil {
		t.Fatalf("create invite: %v", err)
	}

	cookie, headers := preSessionPair(t, srv, "/register")
	first := postJSON(srv, "/api/v1/auth/register", map[string]string{
		"username": "invitee", "email": "invitee@example.com",
		"password": "Sup3rSecret!", "invite": inv.Token,
	}, []*http.Cookie{cookie}, headers)
	if first.Code != http.StatusOK {
		t.Fatalf("invite registration status = %d, want 200 (body %s)", first.Code, snippet(first.Body.String()))
	}

	second := postJSON(srv, "/api/v1/auth/register", map[string]string{
		"username": "invitee2", "email": "invitee2@example.com",
		"password": "Sup3rSecret!", "invite": inv.Token,
	}, []*http.Cookie{cookie}, headers)
	if second.Code != http.StatusForbidden || apiErrorCode(t, second) != "invite_invalid" {
		t.Fatalf("reused invite = %d %s, want 403 invite_invalid", second.Code, apiErrorCode(t, second))
	}

	if n := countUsersByEmail(t, srv, "invitee@example.com"); n != 1 {
		t.Errorf("users from one invite = %d, want 1", n)
	}
	used, err := invites.ByToken(context.Background(), inv.Token)
	if err != nil {
		t.Fatalf("ByToken() error = %v", err)
	}
	if used.UsedAt == nil || used.UsedBy == nil {
		t.Errorf("invite after acceptance = %+v, want used_at and used_by set", used)
	}
}

// TestRegisterAPI_Validation asserts form-level validation returns stable codes and creates no account.
func TestRegisterAPI_Validation(t *testing.T) {
	cases := []struct {
		name     string
		body     map[string]string
		wantCode string
	}{
		{name: "missing username",
			body:     map[string]string{"email": "a@example.com", "password": "Sup3rSecret!"},
			wantCode: "username_required"},
		{name: "invalid email",
			body:     map[string]string{"username": "a", "email": "not-an-email", "password": "Sup3rSecret!"},
			wantCode: "email_invalid"},
		{name: "short password",
			body:     map[string]string{"username": "a", "email": "a@example.com", "password": "short"},
			wantCode: "password_too_short"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := newAuthServer(t)
			seedAdminUser(t, srv)
			writeSetting(t, db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, "open")))
			cookie, headers := preSessionPair(t, srv, "/register")
			rec := postJSON(srv, "/api/v1/auth/register", tc.body, []*http.Cookie{cookie}, headers)
			if rec.Code != http.StatusBadRequest || apiErrorCode(t, rec) != tc.wantCode {
				t.Fatalf("status/code = %d %s, want 400 %s", rec.Code, apiErrorCode(t, rec), tc.wantCode)
			}
		})
	}
}

// TestSetupAPI_CreatesFirstAdminAndCloses asserts the bootstrap JSON endpoint creates an admin when there is none,
// after which the same endpoint is 404 (the one-time admin gate), and success issues no session cookie.
func TestSetupAPI_CreatesFirstAdminAndCloses(t *testing.T) {
	srv, _ := newAuthServer(t)
	cookie, headers := preSessionPair(t, srv, "/setup")

	rec := postJSON(srv, "/api/v1/auth/setup", map[string]string{
		"username": "root", "email": "root@example.com", "password": "Sup3rSecret!",
	}, []*http.Cookie{cookie}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if findCookie(rec, srv.sessions.CookieName()) != nil {
		t.Errorf("setup must not issue a session cookie")
	}
	u, err := srv.users.ByUsername(context.Background(), "root")
	if err != nil {
		t.Fatalf("ByUsername(root) error = %v", err)
	}
	if u.Role != store.RoleAdmin {
		t.Errorf("bootstrap user role = %q, want admin", u.Role)
	}

	// An admin already exists: the same endpoint must be 404 and must not create a second account.
	again := postJSON(srv, "/api/v1/auth/setup", map[string]string{
		"username": "root2", "email": "root2@example.com", "password": "Sup3rSecret!",
	}, []*http.Cookie{cookie}, headers)
	if again.Code != http.StatusNotFound {
		t.Fatalf("second setup status = %d, want 404", again.Code)
	}
	if n := countUsersByEmail(t, srv, "root2@example.com"); n != 0 {
		t.Errorf("closed setup created %d users, want 0", n)
	}
}

// TestSetupAPI_BootstrapEmailFallback asserts BOOTSTRAP_ADMIN_EMAIL is used when the request leaves the email blank,
// with fallback behavior matching the bootstrap page's app shell entry.
func TestSetupAPI_BootstrapEmailFallback(t *testing.T) {
	srv, _ := newAuthServer(t)
	srv.bootstrapEmail = "boot@example.com"
	cookie, headers := preSessionPair(t, srv, "/setup")

	rec := postJSON(srv, "/api/v1/auth/setup", map[string]string{
		"username": "bootroot", "password": "Sup3rSecret!",
	}, []*http.Cookie{cookie}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup with bootstrap email status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if n := countUsersByEmail(t, srv, "boot@example.com"); n != 1 {
		t.Errorf("users with bootstrap email = %d, want 1", n)
	}
}

// TestSetupAPI_CSRFRejection asserts the bootstrap JSON endpoint likewise enforces pre-session double-submit CSRF.
func TestSetupAPI_CSRFRejection(t *testing.T) {
	srv, _ := newAuthServer(t)
	rec := postJSON(srv, "/api/v1/auth/setup", map[string]string{
		"username": "root", "email": "root@example.com", "password": "Sup3rSecret!",
	}, nil, nil)
	if rec.Code != http.StatusForbidden || apiErrorCode(t, rec) != "csrf_failed" {
		t.Fatalf("setup without CSRF = %d %s, want 403 csrf_failed", rec.Code, apiErrorCode(t, rec))
	}
	if n := countUsersByEmail(t, srv, "root@example.com"); n != 0 {
		t.Errorf("CSRF-rejected setup created %d users, want 0", n)
	}
}

// TestRegistrationInfoEndpoint asserts GET /api/v1/auth/registration reports the policy from the same source as the registration endpoint:
// open / invite / closed are returned as-is, and unset or unknown values are normalized to closed (the safe default).
// This endpoint is a pre-login read-only probe: callable anonymously (no cookie), issuing no session cookie and establishing no session.
func TestRegistrationInfoEndpoint(t *testing.T) {
	cases := []struct {
		name    string
		setting string // empty string means do not write a setting, taking the "unset" path
		want    string
	}{
		{name: "unset falls back to closed", want: auth.PolicyClosed},
		{name: "unknown value falls back to closed", setting: "nonsense", want: auth.PolicyClosed},
		{name: "open reported as-is", setting: auth.PolicyOpen, want: auth.PolicyOpen},
		{name: "invite reported as-is", setting: auth.PolicyInvite, want: auth.PolicyInvite},
		{name: "closed reported as-is", setting: auth.PolicyClosed, want: auth.PolicyClosed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := newAuthServer(t)
			seedAdminUser(t, srv)
			if tc.setting != "" {
				writeSetting(t, db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, tc.setting)))
			}

			// No cookie at all: the probe must be usable by anonymous visitors.
			rec := get(t, srv, "/api/v1/auth/registration", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /api/v1/auth/registration status = %d, want 200 (body %s)",
					rec.Code, snippet(rec.Body.String()))
			}
			var resp struct {
				Policy string `json:"policy"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal registration info: %v (body %s)", err, snippet(rec.Body.String()))
			}
			if resp.Policy != tc.want {
				t.Errorf("policy = %q, want %q", resp.Policy, tc.want)
			}
			if findCookie(rec, srv.sessions.CookieName()) != nil {
				t.Errorf("GET /api/v1/auth/registration must not issue a session cookie")
			}
		})
	}
}

// TestRegistrationInfoEndpointMatchesSubmitInBootstrapWindow asserts the probe and submission share one source:
// with no active admin the registration endpoint makes this account an admin (policy plays no part), so the probe must answer open too,
// otherwise a new instance would show "the page says self-registration is unavailable but submitting still creates an admin".
func TestRegistrationInfoEndpointMatchesSubmitInBootstrapWindow(t *testing.T) {
	srv, db := newAuthServer(t)
	// The policy is closed, but the bootstrap window takes precedence over the policy — the probe must report that faithfully.
	writeSetting(t, db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, auth.PolicyClosed)))

	rec := get(t, srv, "/api/v1/auth/registration", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var resp struct {
		Policy string `json:"policy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal registration info: %v", err)
	}
	if resp.Policy != auth.PolicyOpen {
		t.Fatalf("policy during the bootstrap window = %q, want %q", resp.Policy, auth.PolicyOpen)
	}

	// The submission really is allowed and the first account is an admin: the probe's answer matches the real behavior.
	cookie, headers := preSessionPair(t, srv, "/register")
	post := postJSON(srv, "/api/v1/auth/register", map[string]string{
		"username": "firstroot", "email": "firstroot@example.com", "password": "Sup3rSecret!",
	}, []*http.Cookie{cookie}, headers)
	if post.Code != http.StatusOK {
		t.Fatalf("bootstrap registration status = %d, want 200 (body %s)", post.Code, snippet(post.Body.String()))
	}
	u, err := srv.users.ByUsername(context.Background(), "firstroot")
	if err != nil {
		t.Fatalf("ByUsername(firstroot) error = %v", err)
	}
	if u.Role != store.RoleAdmin {
		t.Errorf("bootstrap user role = %q, want admin", u.Role)
	}

	// Once the bootstrap window closes, the same closed policy takes effect and the probe changes its answer accordingly.
	after := get(t, srv, "/api/v1/auth/registration", nil)
	if err := json.Unmarshal(after.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal registration info: %v", err)
	}
	if resp.Policy != auth.PolicyClosed {
		t.Errorf("policy after the bootstrap window = %q, want %q", resp.Policy, auth.PolicyClosed)
	}
}
