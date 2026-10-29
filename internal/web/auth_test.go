package web

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// testEncryptionKey is the AES-GCM master key used in tests (base64 of 32 bytes), unrelated to production.
func testEncryptionKey() string {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = 0x2b
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// testSessionSecret is the session signing secret used in tests; long enough and unrelated to production.
var testSessionSecret = []byte("test-session-secret-0123456789abcdef")

// newAuthServer builds a test service wired with account, session and user stores, and returns the underlying DB.
// Trusts no proxy (the default), so the request IP always comes from RemoteAddr.
func newAuthServer(t *testing.T) (*Server, *gorm.DB) {
	t.Helper()
	return newAuthServerWithProxies(t, nil)
}

// newAuthServerWithProxies is the same as newAuthServer but injects an explicit trusted proxy list,
// used to verify X-Forwarded-For is only honored when it comes from a trusted proxy.
func newAuthServerWithProxies(t *testing.T, trustedProxies []string) (*Server, *gorm.DB) {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	users := store.NewUserStore(db)
	sessions := store.NewSessionStore(db)
	invites := store.NewInviteStore(db)
	accounts, err := auth.NewAccountService(users, sessions, store.NewAPIKeyStore(db), auth.NewPasswordHasher(auth.Params{
		// Weak parameters for tests, so hashing is not slow every time.
		Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32,
	}))
	if err != nil {
		t.Fatalf("NewAccountService() error = %v", err)
	}
	mgr, err := auth.NewSessionManager(users, sessions, auth.SessionConfig{Secret: testSessionSecret})
	if err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}
	auditor, err := auth.NewAuditor(store.NewAuditStore(db), nil)
	if err != nil {
		t.Fatalf("NewAuditor() error = %v", err)
	}
	// Inject an empty wait into the limiter: the test asserts the delay value (see the unit test in internal/auth), so it need not actually sleep here.
	limiter := auth.NewLoginLimiter(auth.LimiterConfig{
		BaseDelay: 5 * time.Millisecond,
		MaxDelay:  50 * time.Millisecond,
		Sleep:     func(context.Context, time.Duration) error { return nil },
	})
	// TOTP two-factor: a real SecretCodec + TOTPStore, so the login second step and the settings API are available.
	codec, err := store.NewSecretCodec(testEncryptionKey())
	if err != nil {
		t.Fatalf("NewSecretCodec() error = %v", err)
	}
	totpSvc, err := auth.NewTOTPService(store.NewTOTPStore(db), codec, auth.DefaultTOTPIssuer)
	if err != nil {
		t.Fatalf("NewTOTPService() error = %v", err)
	}
	srv, err := New("127.0.0.1:0", Deps{
		DB:            db,
		Logger:        discardLogger(),
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		Accounts:      accounts,
		Sessions:      mgr,
		Users:         users,
		Invites:       invites,
		Auditor:       auditor,
		LoginLimiter:  limiter,
		TOTP:          totpSvc,

		TrustedProxies: trustedProxies,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return srv, db
}

// testDoubleSubmitToken is the fixed pre-session CSRF token used in tests; the value only needs to meet the minimum length.
const testDoubleSubmitToken = "test-double-submit-token-0123456789"

// postForm submits a form as application/x-www-form-urlencoded.
//
// After the SSR pre-session forms were removed, page write requests all go through same-origin JSON endpoints (postJSON + double-submit headers),
// and this helper only serves the form write endpoints that still exist (e.g. /settings/password, /admin/*, /unsubscribe).
func postForm(t *testing.T, srv *Server, target string, values url.Values, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// getWithCookies issues a GET with cookies.
func getWithCookies(t *testing.T, srv *Server, target string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// loginJSON goes through the SPA's same-origin JSON login (POST /api/v1/auth/login) and returns the session cookie and the session-bound CSRF token.
// The pre-session double-submit cookie/mirror token is issued by the GET /login app shell. Fatals on login failure.
func loginJSON(t *testing.T, srv *Server, db *gorm.DB, username, password string) ([]*http.Cookie, string) {
	t.Helper()
	cookie, headers := preSessionPair(t, srv, "/login")
	rec := postJSON(srv, "/api/v1/auth/login", map[string]string{
		"username": username, "password": password,
	}, []*http.Cookie{cookie}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/login (%s) = %d, want 200 (body %s)", username, rec.Code, snippet(rec.Body.String()))
	}
	var sess store.Session
	if err := db.Order("created_at desc").First(&sess).Error; err != nil {
		t.Fatalf("load session row: %v", err)
	}
	if sess.CSRFToken == "" {
		t.Fatal("session row has no CSRF token")
	}
	return rec.Result().Cookies(), sess.CSRFToken
}

// setupFirstAdmin creates the first admin through the JSON bootstrap endpoint; the double-submit cookie is taken from the bootstrap window it opens.
// The caller asserts the response code.
func setupFirstAdmin(t *testing.T, srv *Server, username, email, password string) *httptest.ResponseRecorder {
	t.Helper()
	cookie, headers := preSessionPair(t, srv, "/setup")
	body := map[string]string{"username": username, "password": password}
	if email != "" {
		body["email"] = email
	}
	return postJSON(srv, "/api/v1/auth/setup", body, []*http.Cookie{cookie}, headers)
}

// TestAuthFlowThroughJSONEndpoints is an acceptance test: bootstrap an admin → JSON login → JSON logout.
// The pages have migrated to the SPA, so the only transport on this chain is /api/v1/auth/{setup,login,session,logout}.
func TestAuthFlowThroughJSONEndpoints(t *testing.T) {
	srv, db := newAuthServer(t)

	// No admin yet: the bootstrap endpoint creates the first admin and issues no session.
	setup := setupFirstAdmin(t, srv, "admin", "admin@example.com", "Sup3rSecret!")
	if setup.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/setup = %d, want 200 (body %s)", setup.Code, snippet(setup.Body.String()))
	}
	if findCookie(setup, srv.sessions.CookieName()) != nil {
		t.Error("setup must not issue a session cookie")
	}

	// Wrong password: 401, no session cookie issued.
	cookie, headers := preSessionPair(t, srv, "/login")
	bad := postJSON(srv, "/api/v1/auth/login", map[string]string{
		"username": "admin", "password": "WrongPassword!",
	}, []*http.Cookie{cookie}, headers)
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("login with a wrong password = %d, want 401", bad.Code)
	}
	if findCookie(bad, srv.sessions.CookieName()) != nil {
		t.Error("failed login issued a session cookie")
	}

	// Correct credentials: 200 + session cookie + session-bound CSRF token.
	cookies, csrf := loginJSON(t, srv, db, "admin", "Sup3rSecret!")

	// The session works: GET /api/v1/auth/session reports signed in.
	me := getWithCookies(t, srv, "/api/v1/auth/session", cookies)
	if !strings.Contains(me.Body.String(), `"authenticated":true`) {
		t.Fatalf("session after login is not authenticated: %s", snippet(me.Body.String()))
	}

	// Logout must carry the session-bound CSRF: no token 403, with token 200 and the session invalidated.
	noCSRF := postJSON(srv, "/api/v1/auth/logout", nil, cookies, nil)
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("logout without a CSRF token = %d, want 403", noCSRF.Code)
	}
	logout := postJSON(srv, "/api/v1/auth/logout", nil, cookies, map[string]string{auth.CSRFHeaderName: csrf})
	if logout.Code != http.StatusOK {
		t.Fatalf("logout = %d, want 200 (body %s)", logout.Code, snippet(logout.Body.String()))
	}
	after := getWithCookies(t, srv, "/api/v1/auth/session", cookies)
	if !strings.Contains(after.Body.String(), `"authenticated":false`) {
		t.Errorf("session is still active after logout: %s", snippet(after.Body.String()))
	}

	var refreshed store.Session
	if err := db.Order("created_at desc").First(&refreshed).Error; err != nil {
		t.Fatalf("reload session row: %v", err)
	}
	if refreshed.RevokedAt == nil {
		t.Error("session row was not revoked after logout")
	}
}

// TestHomeRedirectsToSetupUntilFirstAdmin is an acceptance test: after installation but before setup,
// GET / 303s to /setup (the bootstrap window); once the first admin exists the home page returns the app shell.
func TestHomeRedirectsToSetupUntilFirstAdmin(t *testing.T) {
	srv, _ := newAuthServer(t)

	rec := get(t, srv, "/", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET / before any admin = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/setup" {
		t.Errorf("Location = %q, want /setup", loc)
	}

	if setup := setupFirstAdmin(t, srv, "root", "root@example.com", "Sup3rSecret!"); setup.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/setup = %d, want 200 (body %s)", setup.Code, snippet(setup.Body.String()))
	}

	after := get(t, srv, "/", nil)
	if after.Code != http.StatusOK {
		t.Fatalf("GET / after setup = %d, want 200 (body %s)", after.Code, snippet(after.Body.String()))
	}
	if !strings.Contains(after.Body.String(), `id="app"`) {
		t.Errorf("home after setup did not serve the SPA shell: %s", snippet(after.Body.String()))
	}
}

// TestSetupUnavailableAfterAdminExists is an acceptance point: the one-time admin gate.
// GET /setup is reachable before there is an admin; afterwards both GET /setup and POST /api/v1/auth/setup must be 404.
func TestSetupUnavailableAfterAdminExists(t *testing.T) {
	srv, _ := newAuthServer(t)

	if code := get(t, srv, "/setup", nil).Code; code != http.StatusOK {
		t.Fatalf("GET /setup before any admin = %d, want 200", code)
	}
	if setup := setupFirstAdmin(t, srv, "root", "root@example.com", "Sup3rSecret!"); setup.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/setup = %d, want 200 (body %s)", setup.Code, snippet(setup.Body.String()))
	}

	if code := get(t, srv, "/setup", nil).Code; code != http.StatusNotFound {
		t.Errorf("GET /setup after an admin exists = %d, want 404", code)
	}
	// The double-submit cookie is taken from the still-reachable /login: the closed bootstrap window no longer issues it.
	cookie, headers := preSessionPair(t, srv, "/login")
	closed := postJSON(srv, "/api/v1/auth/setup", map[string]string{
		"username": "second", "email": "second@example.com", "password": "Sup3rSecret!",
	}, []*http.Cookie{cookie}, headers)
	if closed.Code != http.StatusNotFound {
		t.Errorf("POST /api/v1/auth/setup after an admin exists = %d, want 404", closed.Code)
	}
}

// Ensure gin is in quiet mode during tests.
func init() { gin.SetMode(gin.TestMode) }
