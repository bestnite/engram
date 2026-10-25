package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

func TestSharingOwnerSessionCSRFAndGrantConsent(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Share SPA")
	path := "/api/v1/decks/" + deck.PublicID + "/sharing"
	owner := getWithCookies(t, srv, path, ownerCookies)
	if owner.Code != http.StatusOK || !strings.Contains(owner.Body.String(), `"pending_invites"`) {
		t.Fatalf("owner read = %d %s", owner.Code, owner.Body.String())
	}
	nonOwnerID, nonOwnerCookies, _ := createUserAndLogin(t, srv, db, "non_owner")
	_ = nonOwnerID
	if got := getWithCookies(t, srv, path, nonOwnerCookies); got.Code != http.StatusForbidden {
		t.Fatalf("non-owner read = %d, want 403", got.Code)
	}
	if got := jsonRequest(t, srv, "POST", path+"/grants", `{"username":"non_owner","role":"reader"}`, ownerCookies, ""); got.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF = %d, want 403", got.Code)
	}
	if got := jsonRequest(t, srv, "POST", path+"/grants", `{"username":"non_owner","role":"reader"}`, ownerCookies, ownerCSRF); got.Code != http.StatusCreated && got.Code != http.StatusOK {
		t.Fatalf("grant = %d: %s", got.Code, got.Body.String())
	}
	if got := jsonRequest(t, srv, "POST", path+"/grants", `{"username":"non_owner","role":"owner"}`, ownerCookies, ownerCSRF); got.Code != http.StatusBadRequest {
		t.Fatalf("owner grant = %d, want 400", got.Code)
	}
	// The visibility endpoint was removed along with "public/private decks": the route does not exist, so writing to it always gives 404.
	if got := jsonRequest(t, srv, "POST", path+"/visibility", `{"visibility":"public"}`, ownerCookies, ownerCSRF); got.Code != http.StatusNotFound {
		t.Fatalf("removed visibility endpoint = %d, want 404", got.Code)
	}
	// Consent model: the sharing write path records an **invite** audit entry (the grant is only written when the other side accepts).
	if n, err := store.NewAuditStore(db).CountByAction(t.Context(), store.ActionDeckShareInvite); err != nil || n < 1 {
		t.Fatalf("share invite audit count=(%d,%v)", n, err)
	}
}

func jsonRequest(t *testing.T, srv *Server, method, path, body string, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}
