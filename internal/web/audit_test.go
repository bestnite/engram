package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// registerJSON goes through the SPA's same-origin JSON registration (POST /api/v1/auth/register), submitting a pair of pre-session double-submit
// cookie and mirror token. The page layer has been removed, so JSON is the only transport for registration.
func registerJSON(t *testing.T, srv *Server, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	cookie, headers := preSessionPair(t, srv, "/register")
	return postJSON(srv, "/api/v1/auth/register", body, []*http.Cookie{cookie}, headers)
}

// TestMutationsWriteExactlyOneAuditRow is a wiring acceptance test: register, login and logout each write one row,
// and the action string matches the constant in store.
func TestMutationsWriteExactlyOneAuditRow(t *testing.T) {
	srv, db := newAuthServer(t)
	ctx := context.Background()

	reg := registerJSON(t, srv, map[string]string{
		"username": "admin",
		"email":    "admin@example.com",
		"password": "Sup3rSecret!",
	})
	if reg.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/register status = %d, want 200 (body %s)", reg.Code, snippet(reg.Body.String()))
	}

	// Login goes through JSON: this also obtains the session-bound CSRF token (a channel other than the page).
	cookies, csrf := loginJSON(t, srv, db, "admin", "Sup3rSecret!")
	logout := postJSON(srv, "/api/v1/auth/logout", nil, cookies, map[string]string{auth.CSRFHeaderName: csrf})
	if logout.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/logout status = %d, want 200", logout.Code)
	}

	audit := store.NewAuditStore(db)
	for action, want := range map[string]int64{
		store.ActionUserCreate:         1,
		store.ActionUserLoginSucceeded: 1,
		store.ActionUserLogout:         1,
	} {
		got, err := audit.CountByAction(ctx, action)
		if err != nil {
			t.Fatalf("CountByAction(%q) error = %v", action, err)
		}
		if got != want {
			t.Errorf("audit rows for %q = %d, want %d", action, got, want)
		}
	}

	createRow, err := audit.List(ctx, 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(createRow) != 3 {
		t.Fatalf("total audit rows = %d, want 3", len(createRow))
	}
}

// TestLoginFailureIsAuditedAndLimiterResets is a wiring acceptance test: a failure writes an audit row and accumulates the rate limit,
// and the rate limit is reset after a successful login.
func TestLoginFailureIsAuditedAndLimiterResets(t *testing.T) {
	srv, db := newAuthServer(t)
	ctx := context.Background()

	reg := registerJSON(t, srv, map[string]string{
		"username": "admin",
		"email":    "admin@example.com",
		"password": "Sup3rSecret!",
	})
	if reg.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/register status = %d, want 200 (body %s)", reg.Code, snippet(reg.Body.String()))
	}

	for i := 0; i < 3; i++ {
		cookie, headers := preSessionPair(t, srv, "/login")
		rec := postJSON(srv, "/api/v1/auth/login", map[string]string{
			"username": "admin",
			"password": "WrongPassword!",
		}, []*http.Cookie{cookie}, headers)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("failed login #%d status = %d, want 401", i+1, rec.Code)
		}
	}

	audit := store.NewAuditStore(db)
	if n, err := audit.CountByAction(ctx, store.ActionUserLoginFailed); err != nil || n != 3 {
		t.Fatalf("user.login_failed audit rows = (%d, %v), want (3, nil)", n, err)
	}
	// The account dimension has accumulated failures: the next attempt must wait (passing an empty IP queries only the account dimension).
	if d, err := srv.loginLimiter.Wait(ctx, "admin", ""); err != nil || d <= 0 {
		t.Fatalf("limiter delay before a successful login = (%v, %v), want > 0", d, err)
	}

	// Successful login (Fatal on failure), and the rate limit resets with it.
	loginJSON(t, srv, db, "admin", "Sup3rSecret!")
	if d, err := srv.loginLimiter.Wait(ctx, "admin", ""); err != nil || d != 0 {
		t.Fatalf("limiter delay after a successful login = (%v, %v), want (0, nil)", d, err)
	}
	if n, err := audit.CountByAction(ctx, store.ActionUserLoginSucceeded); err != nil || n != 1 {
		t.Fatalf("user.login_succeeded audit rows = (%d, %v), want (1, nil)", n, err)
	}
}
