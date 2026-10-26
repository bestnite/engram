package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// sessionFor starts a session directly for a user that cannot log in with a password (an OIDC account).
func sessionFor(t *testing.T, srv *Server, userID uint64) ([]*http.Cookie, string) {
	t.Helper()
	dummyRec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(dummyRec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	sess, err := srv.sessions.StartSession(context.Background(), c, userID)
	if err != nil {
		t.Fatalf("StartSession(%d): %v", userID, err)
	}
	cookie := findCookie(dummyRec, srv.sessions.CookieName())
	if cookie == nil {
		t.Fatal("StartSession did not issue a session cookie")
	}
	return []*http.Cookie{cookie}, sess.CSRFToken
}

func TestInitialPasswordForPasswordlessAccount(t *testing.T) {
	ctx := context.Background()
	srv, db := newAuthServer(t)
	u, err := srv.accounts.CreateOIDCUser(ctx, auth.CreateUserInput{
		Username: "oidc-user", Email: "oidc-user@example.com",
	})
	if err != nil {
		t.Fatalf("CreateOIDCUser: %v", err)
	}
	cookies, csrf := sessionFor(t, srv, u.ID)
	// A second session on another device must be revoked by the first-password change.
	otherCookies, _ := sessionFor(t, srv, u.ID)

	rec := patchProfile(t, srv, "/api/v1/settings/password", passwordRequest{NewPassword: "N3wStrongPassword!"}, cookies, csrf, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("first password = %d, want 204 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	stored, err := store.NewUserStore(db).ByID(ctx, u.ID)
	if err != nil || stored.PasswordHash == nil {
		t.Fatalf("password hash not stored: %+v, err=%v", stored, err)
	}
	if _, err := srv.accounts.Authenticate(ctx, "oidc-user", "N3wStrongPassword!"); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
	if rec := getWithCookies(t, srv, "/api/v1/profile", cookies); rec.Code != http.StatusOK {
		t.Fatalf("current session was revoked: %d", rec.Code)
	}
	if rec := getWithCookies(t, srv, "/api/v1/profile", otherCookies); rec.Code != http.StatusUnauthorized {
		t.Fatalf("other session still valid: %d", rec.Code)
	}

	rows, err := store.NewAuditStore(db).List(ctx, 50)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	found := false
	for _, row := range rows {
		if row.Action != store.ActionUserPasswordChange {
			continue
		}
		found = true
		if row.DetailJSON == nil || !containsJSONField(*row.DetailJSON, "initial", true) {
			t.Fatalf("password audit detail = %v, want initial=true", row.DetailJSON)
		}
	}
	if !found {
		t.Fatal("no password change audit row")
	}

	// A second submission from a tab that loaded the page while passwordless sends no old password. The password now exists, so this is the
	// stale-page conflict, not a malformed request, and the stored hash must not change.
	firstHash := *stored.PasswordHash
	rec = patchProfile(t, srv, "/api/v1/settings/password", passwordRequest{NewPassword: "An0therStrongPass!"}, cookies, csrf, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("password change without the old password = %d, want 409 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var conflict struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeJSON(t, rec, &conflict)
	if conflict.Error.Code != "password_already_set" {
		t.Fatalf("stale password code = %q, want password_already_set", conflict.Error.Code)
	}
	stored, err = store.NewUserStore(db).ByID(ctx, u.ID)
	if err != nil || stored.PasswordHash == nil || *stored.PasswordHash != firstHash {
		t.Fatalf("stale request changed the password hash: %v, err=%v", stored.PasswordHash, err)
	}
}

// containsJSONField reports whether the JSON object in raw has key set to want.
func containsJSONField(raw, key string, want any) bool {
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return false
	}
	return obj[key] == want
}
