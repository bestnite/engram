package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// postLoginFrom performs a JSON login (POST /api/v1/auth/login) with the given RemoteAddr and extra request headers,
// filling in the pre-session double-submit CSRF (cookie + mirrored header).
// Both the rate limiter's and the audit's IP come from gin's ClientIP, and whether ClientIP trusts X-Forwarded-For depends on the trusted proxy config,
// so the test must be able to control RemoteAddr and XFF at the same time.
func postLoginFrom(t *testing.T, srv *Server, username, password, remoteAddr string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		t.Fatalf("marshal login body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(auth.CSRFHeaderName, testDoubleSubmitToken)
	req.AddCookie(&http.Cookie{Name: auth.CSRFDoubleSubmitCookieName, Value: testDoubleSubmitToken})
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// latestLoginFailedIP reads back the ip field of the most recent login-failure audit entry.
func latestLoginFailedIP(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var row store.AuditLog
	if err := db.Where("action = ?", store.ActionUserLoginFailed).Order("id DESC").First(&row).Error; err != nil {
		t.Fatalf("load login_failed audit row: %v", err)
	}
	if row.DetailJSON == nil {
		t.Fatal("login_failed audit row has no detail_json")
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(*row.DetailJSON), &detail); err != nil {
		t.Fatalf("unmarshal audit detail: %v", err)
	}
	ip, _ := detail["ip"].(string)
	return ip
}

// TestLoginAuditAndLimiterIgnoreForwardedForByDefault is the core negative assertion of this fix:
// with TRUSTED_PROXIES unset no proxy may be trusted, and a forged X-Forwarded-For can neither rewrite the audit IP
// nor steal the login rate-limit key away from the real source.
//
// How it asserts: the ip column of the audit row (the observable end result) plus the limiter's Wait return value (a different username isolates the account dimension,
// leaving only the IP dimension), avoiding reliance on the limiter's internal state.
func TestLoginAuditAndLimiterIgnoreForwardedForByDefault(t *testing.T) {
	srv, db := newAuthServer(t)
	ctx := context.Background()

	const remoteAddr = "192.0.2.1:12345"
	const forged = "1.2.3.4"

	rec := postLoginFrom(t, srv, "attacker", "WrongPassword!", remoteAddr,
		map[string]string{"X-Forwarded-For": forged})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /login status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}

	if got := latestLoginFailedIP(t, db); got != "192.0.2.1" {
		t.Errorf("audit ip = %q, want %q (RemoteAddr) — forged X-Forwarded-For must be ignored", got, "192.0.2.1")
	}

	// Query Wait with a nonexistent username to isolate the IP dimension: the key must land on the real source IP.
	if d, err := srv.loginLimiter.Wait(ctx, "someone-else", "192.0.2.1"); err != nil || d <= 0 {
		t.Errorf("limiter keyed by RemoteAddr = (%v, %v), want delay > 0", d, err)
	}
	if d, err := srv.loginLimiter.Wait(ctx, "someone-else", forged); err != nil || d != 0 {
		t.Errorf("limiter keyed by forged XFF = (%v, %v), want (0, nil)", d, err)
	}
}

// TestLoginAdoptsForwardedForFromTrustedProxy asserts the only path that is trusted: when the source falls inside the trusted proxy list,
// X-Forwarded-For takes effect, and both the audit IP and the rate-limit key use it.
func TestLoginAdoptsForwardedForFromTrustedProxy(t *testing.T) {
	srv, db := newAuthServerWithProxies(t, []string{"127.0.0.1/32"})
	ctx := context.Background()

	const remoteAddr = "127.0.0.1:54321"
	const forwarded = "198.51.100.7"

	rec := postLoginFrom(t, srv, "attacker", "WrongPassword!", remoteAddr,
		map[string]string{"X-Forwarded-For": forwarded})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /login status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}

	if got := latestLoginFailedIP(t, db); got != forwarded {
		t.Errorf("audit ip = %q, want %q (X-Forwarded-For from a trusted proxy)", got, forwarded)
	}
	if d, err := srv.loginLimiter.Wait(ctx, "someone-else", forwarded); err != nil || d <= 0 {
		t.Errorf("limiter keyed by forwarded IP = (%v, %v), want delay > 0", d, err)
	}
	if d, err := srv.loginLimiter.Wait(ctx, "someone-else", "127.0.0.1"); err != nil || d != 0 {
		t.Errorf("limiter keyed by proxy addr = (%v, %v), want (0, nil)", d, err)
	}
}

// TestForwardedForIgnoredWhenRemoteIsNotATrustedProxy asserts that even with trusted proxies configured, arbitrary sources are not let through:
// only an XFF sent from an address on the list is trusted.
func TestForwardedForIgnoredWhenRemoteIsNotATrustedProxy(t *testing.T) {
	srv, db := newAuthServerWithProxies(t, []string{"127.0.0.1/32"})

	rec := postLoginFrom(t, srv, "attacker", "WrongPassword!", "192.0.2.1:12345",
		map[string]string{"X-Forwarded-For": "1.2.3.4"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /login status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
	if got := latestLoginFailedIP(t, db); got != "192.0.2.1" {
		t.Errorf("audit ip = %q, want %q — an untrusted remote must not be able to spoof it", got, "192.0.2.1")
	}
}

// TestNewRejectsInvalidTrustedProxy asserts construction fails on an illegal entry and names it, rather than silently ignoring it.
func TestNewRejectsInvalidTrustedProxy(t *testing.T) {
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "proxy.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	srv, err := New("127.0.0.1:0", Deps{
		DB:             db,
		Logger:         discardLogger(),
		SchemaVersion:  func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		TrustedProxies: []string{"203.0.113.0/33"},
	})
	if err == nil {
		t.Fatalf("New() = %v, want a refusal naming the invalid proxy entry", srv)
	}
	if !strings.Contains(err.Error(), "203.0.113.0/33") {
		t.Errorf("New() error = %q, want it to name %q", err.Error(), "203.0.113.0/33")
	}
}
