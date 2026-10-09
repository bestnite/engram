package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

func TestSharingOwnerSessionCSRFAndLinkSecrecy(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Share SPA")
	path := "/api/v1/decks/" + u64str(deck.ID) + "/sharing"
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
	link := jsonRequest(t, srv, "POST", path+"/links", `{}`, ownerCookies, ownerCSRF)
	if link.Code != http.StatusCreated || !strings.Contains(link.Body.String(), `"link":"/s/`) {
		t.Fatalf("create link = %d %s", link.Code, link.Body.String())
	}
	var createBody struct {
		Link string `json:"link"`
	}
	if err := json.Unmarshal(link.Body.Bytes(), &createBody); err != nil {
		t.Fatal(err)
	}
	plaintext := strings.TrimPrefix(createBody.Link, "/s/")
	if got := jsonRequest(t, srv, "GET", "/s/"+plaintext, "", ownerCookies, ""); got.Code != http.StatusOK {
		t.Fatalf("active link = %d, want 200", got.Code)
	}
	if got := jsonRequest(t, srv, "DELETE", path+"/links/revoke/"+store.ShareLinkDigest(plaintext), "", ownerCookies, ownerCSRF); got.Code != http.StatusOK {
		t.Fatalf("revoke link = %d", got.Code)
	}
	if got := jsonRequest(t, srv, "GET", "/s/"+plaintext, "", ownerCookies, ""); got.Code != http.StatusNotFound {
		t.Fatalf("revoked link = %d, want 404", got.Code)
	}
	listed := getWithCookies(t, srv, path, ownerCookies)
	if strings.Contains(listed.Body.String(), "/s/") {
		t.Fatalf("link plaintext leaked in listing: %s", listed.Body.String())
	}
	// 可见性端点已随「公开/不公开卡组」一起删除：路由不存在，写它一律 404。
	if got := jsonRequest(t, srv, "POST", path+"/visibility", `{"visibility":"public"}`, ownerCookies, ownerCSRF); got.Code != http.StatusNotFound {
		t.Fatalf("removed visibility endpoint = %d, want 404", got.Code)
	}
	// 同意制：共享写路径落的是**邀请**审计（授权要等对方接受时才写）。
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
