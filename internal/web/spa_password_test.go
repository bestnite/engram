package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

func TestSPAPasswordChangeSecurityFlow(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	post := func(old, next, token string) *httptest.ResponseRecorder {
		return patchProfile(t, srv, "/api/v1/settings/password", spaPasswordRequest{OldPassword: old, NewPassword: next}, cookies, token, "")
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
	if rec := post("Sup3rSecret!", "N3wStrongPassword!", csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("password change: %d %s", rec.Code, rec.Body.String())
	}
	if rec := getWithCookies(t, srv, "/api/v1/profile", cookies); rec.Code != http.StatusOK {
		t.Fatalf("current session was revoked: %d", rec.Code)
	}
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
