package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

func TestPasswordChangeSecurityFlow(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	post := func(old, next, token string) *httptest.ResponseRecorder {
		return patchProfile(t, srv, "/api/v1/settings/password", passwordRequest{OldPassword: old, NewPassword: next}, cookies, token, "")
	}
	if rec := post("wrong", "N3wStrongPassword!", csrf); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current password: %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("Sup3rSecret!", "short", csrf); rec.Code != http.StatusBadRequest {
		t.Fatalf("weak password: %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("Sup3rSecret!", "N3wStrongPassword!", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF: %d", rec.Code)
	}
	rec := post("Sup3rSecret!", "N3wStrongPassword!", csrf)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("password change: %d %s", rec.Code, rec.Body.String())
	}
	rotatedPasswordSession(t, srv, rec, cookies, csrf)
	if _, err := srv.accounts.Authenticate(context.Background(), "owner", "Sup3rSecret!"); err == nil {
		t.Fatal("old password authenticated")
	}
	if _, err := srv.accounts.Authenticate(context.Background(), "owner", "N3wStrongPassword!"); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
	if count, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionUserPasswordChange); err != nil || count != 1 {
		t.Fatalf("password audit = %d, %v", count, err)
	}
}

func rotatedPasswordSession(t *testing.T, srv *Server, response *httptest.ResponseRecorder, oldCookies []*http.Cookie, oldCSRF string) ([]*http.Cookie, string) {
	t.Helper()
	if rec := getWithCookies(t, srv, "/api/v1/profile", oldCookies); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old session profile = %d, want 401", rec.Code)
	}
	cookie := findCookie(response, srv.sessions.CookieName())
	if cookie == nil || cookie.Value == "" {
		t.Fatal("password change did not issue a replacement session cookie")
	}
	cookies := []*http.Cookie{cookie}
	if rec := getWithCookies(t, srv, "/api/v1/profile", cookies); rec.Code != http.StatusOK {
		t.Fatalf("replacement session profile = %d, want 200", rec.Code)
	}
	stale := patchProfile(t, srv, "/api/v1/settings/password", passwordRequest{NewPassword: "An0therStrongPass!"}, cookies, oldCSRF, "")
	if stale.Code != http.StatusForbidden || apiErrorCode(t, stale) != "csrf_failed" {
		t.Fatalf("replacement session with old CSRF = %d %s, want 403 csrf_failed", stale.Code, stale.Body.String())
	}
	session := getWithCookies(t, srv, "/api/v1/auth/session", cookies)
	if session.Code != http.StatusOK {
		t.Fatalf("replacement session endpoint = %d %s", session.Code, session.Body.String())
	}
	var body struct {
		CSRFToken string `json:"csrf_token"`
	}
	decodeJSON(t, session, &body)
	if body.CSRFToken == "" || body.CSRFToken == oldCSRF {
		t.Fatal("replacement session did not return a fresh CSRF token")
	}
	return cookies, body.CSRFToken
}
