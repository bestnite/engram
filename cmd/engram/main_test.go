package main

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/config"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web"
)

// testSessionSecret is the session signing secret used by the integration tests; test-only, unrelated to production.
const testSessionSecret = "integration-test-session-secret-0123456789"

// testEncryptionKey is the master key used by the integration tests: base64(32 bytes), test-only, unrelated to production.
var testEncryptionKey = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

// testDoubleSubmitToken is the pre-session CSRF token used by the integration tests; the value only has to meet the minimum length.
const testDoubleSubmitToken = "test-double-submit-token-0123456789"

// newWiredServer constructs the server through the same assembly path as runServe, verifying that the wiring really
// registers auth routes such as /login and /setup in the process, and not only in the web package's unit tests.
func newWiredServer(t *testing.T) (*web.Server, *store.UserStore) {
	t.Helper()
	t.Setenv("DB_DRIVER", "sqlite")
	t.Setenv("DB_DSN", filepath.Join(t.TempDir(), "wiring.db"))
	t.Setenv("SESSION_SECRET", testSessionSecret)
	t.Setenv("ENCRYPTION_KEY", testEncryptionKey)
	t.Setenv("BOOTSTRAP_ADMIN_EMAIL", "bootstrap@example.com")
	t.Setenv("BASE_URL", "http://localhost:8080")

	cfg, err := config.Load(os.LookupEnv, nil)
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	db, err := store.Open(cfg.Get(config.KeyDBDriver).Value, cfg.Get(config.KeyDBDSN).Value)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("store.AutoMigrate() error = %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := newWebServer(cfg, db, logger)
	if err != nil {
		t.Fatalf("newWebServer() error = %v", err)
	}
	return srv, store.NewUserStore(db)
}

// doGet issues a GET and returns the recorder.
func doGet(t *testing.T, srv *web.Server, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// doPostJSON issues a POST as application/json and, following the pre-session flow, fills in the double-submit cookie and the
// mirror header (/api/v1/auth/login, /setup and so on are protected by DoubleSubmitMiddleware).
func doPostJSON(t *testing.T, srv *web.Server, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(auth.CSRFHeaderName, testDoubleSubmitToken)
	req.AddCookie(&http.Cookie{Name: auth.CSRFDoubleSubmitCookieName, Value: testDoubleSubmitToken})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// hasCookie reports whether the response set a cookie with the given name.
func hasCookie(rec *httptest.ResponseRecorder, name string) bool {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name && c.Value != "" {
			return true
		}
	}
	return false
}

// TestWiredServerExposesAuthRoutes is the acceptance test: under the real assembly path /login, /setup and
// /healthz are all reachable, /setup becomes 404 by contract once the first administrator exists, and a wrong password returns 401 by contract.
func TestWiredServerExposesAuthRoutes(t *testing.T) {
	srv, users := newWiredServer(t)

	// /healthz still works: the wiring must not break M0's health check.
	if rec := doGet(t, srv, "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// No administrator yet: the onboarding page is reachable. GET /setup has switched to the SPA app shell (the canonical path),
	// so this asserts "reachable + is the app shell + set the pre-session double-submit cookie"; the SSR onboarding page's own
	// rendering details (localized copy, BOOTSTRAP_ADMIN_EMAIL prefill) are covered by internal/web's fallback-branch cases.
	setup := doGet(t, srv, "/setup")
	if setup.Code != http.StatusOK {
		t.Fatalf("GET /setup with no admin status = %d, want 200 (body %s)", setup.Code, setup.Body.String())
	}
	if !strings.Contains(setup.Body.String(), `id="app"`) {
		t.Errorf("GET /setup did not serve the SPA shell: %s", snippet(setup.Body.String()))
	}
	if !hasCookie(setup, auth.CSRFDoubleSubmitCookieName) {
		t.Errorf("GET /setup did not issue the %s cookie", auth.CSRFDoubleSubmitCookieName)
	}

	// /login has likewise switched to the SPA app shell.
	login := doGet(t, srv, "/login")
	if login.Code != http.StatusOK {
		t.Fatalf("GET /login status = %d, want 200 (body %s)", login.Code, login.Body.String())
	}
	if !strings.Contains(login.Body.String(), `id="app"`) {
		t.Errorf("GET /login did not serve the SPA shell: %s", snippet(login.Body.String()))
	}
	if !hasCookie(login, auth.CSRFDoubleSubmitCookieName) {
		t.Errorf("GET /login did not issue the %s cookie", auth.CSRFDoubleSubmitCookieName)
	}

	// Wrong password: JSON login returns 401 and sets no cookie at all (the SSR login form went away with the page layer).
	bad := doPostJSON(t, srv, "/api/v1/auth/login", `{"username":"nobody","password":"WrongPassword!"}`)
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("POST /api/v1/auth/login with a wrong password status = %d, want 401 (body %s)", bad.Code, snippet(bad.Body.String()))
	}
	if len(bad.Result().Cookies()) != 0 {
		t.Errorf("failed login unexpectedly set a cookie: %v", bad.Result().Cookies())
	}

	// Create the first administrator; the onboarding page must then close (404).
	created := doPostJSON(t, srv, "/api/v1/auth/setup",
		`{"username":"root","email":"root@example.com","password":"Sup3rSecret!"}`)
	if created.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/setup status = %d, want 200 (body %s)", created.Code, snippet(created.Body.String()))
	}
	if n, err := users.CountActiveAdmins(context.Background()); err != nil || n != 1 {
		t.Fatalf("CountActiveAdmins() = %d, %v; want 1, nil", n, err)
	}
	if rec := doGet(t, srv, "/setup"); rec.Code != http.StatusNotFound {
		t.Errorf("GET /setup after an admin exists status = %d, want 404", rec.Code)
	}
}

// snippet truncates the response body so failure output stays short.
func snippet(body string) string {
	const max = 300
	if len(body) <= max {
		return body
	}
	return body[:max] + "..."
}

// TestWiredServerStartsWithMailOutbox verifies the wiring: under the real assembly path the Server exposes the
// mail outbox and SMTP is unconfigured by default (Configured()=false), and the worker can start and stop gracefully.
func TestWiredServerStartsWithMailOutbox(t *testing.T) {
	srv, _ := newWiredServer(t)
	ob := srv.Mail()
	if ob == nil {
		t.Fatal("wired server has no mail outbox; the SMTP worker would never run")
	}
	if ob.Configured() {
		t.Error("mail outbox reports configured with no SMTP settings, want false")
	}
	// Start/stop must be idempotent and must not hang.
	ctx, cancel := context.WithCancel(context.Background())
	ob.Start(ctx)
	ob.Stop()
	cancel()
}
