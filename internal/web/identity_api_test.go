package web

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// This file covers the self-service identity settings API: listing, binding and unlinking the caller's own OIDC identities,
// plus setting a first password for a passwordless account. The stub provider in oidc_test.go drives the real bind callback.

func identityRows(t *testing.T, db *gorm.DB, userID uint64) []store.Identity {
	t.Helper()
	rows, err := store.NewIdentityStore(db).ListForUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListForUser(%d): %v", userID, err)
	}
	return rows
}

func TestIdentityListOnlyOwnRows(t *testing.T) {
	ctx := context.Background()
	srv, db := newAuthServer(t)
	owner := createTestUser(t, srv, "owner", "Sup3rSecret!", store.RoleUser, true)
	other := createTestUser(t, srv, "other", "Sup3rSecret!", store.RoleUser, true)
	identities := store.NewIdentityStore(db)
	email := "owner@example.com"
	if err := identities.Create(ctx, &store.Identity{UserID: owner.ID, Provider: "https://issuer.example.com", Subject: "sub-owner", Email: &email}); err != nil {
		t.Fatalf("seed owner identity: %v", err)
	}
	if err := identities.Create(ctx, &store.Identity{UserID: other.ID, Provider: "https://issuer.example.com", Subject: "sub-other"}); err != nil {
		t.Fatalf("seed other identity: %v", err)
	}

	cookies, _ := loginJSON(t, srv, db, "owner", "Sup3rSecret!")
	rec := getWithCookies(t, srv, "/api/v1/settings/identities", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET identities = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var resp identityListResponse
	decodeJSON(t, rec, &resp)
	if resp.OIDCEnabled {
		t.Error("oidc_enabled = true while OIDC is unconfigured")
	}
	if len(resp.Identities) != 1 || resp.Identities[0].ID != identityRows(t, db, owner.ID)[0].PublicID {
		t.Fatalf("identities = %+v, want only the owner's row", resp.Identities)
	}
	if resp.Identities[0].Email != email || resp.Identities[0].Provider != "https://issuer.example.com" || resp.Identities[0].LinkedAt == "" {
		t.Fatalf("identity payload = %+v", resp.Identities[0])
	}
}

func TestIdentityListReportsOIDCEnabled(t *testing.T) {
	srv, db := newAuthServer(t)
	p := newStubOIDC(t)
	seedOIDC(t, srv, db, p, auth.PolicyClosed)
	createTestUser(t, srv, "owner", "Sup3rSecret!", store.RoleUser, true)
	cookies, _ := loginJSON(t, srv, db, "owner", "Sup3rSecret!")

	rec := getWithCookies(t, srv, "/api/v1/settings/identities", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET identities = %d, want 200", rec.Code)
	}
	var resp identityListResponse
	decodeJSON(t, rec, &resp)
	if !resp.OIDCEnabled {
		t.Error("oidc_enabled = false while OIDC is usable")
	}
	if resp.Identities == nil {
		t.Error("identities is null, want an empty array")
	}
}

func TestIdentityBindStartCreatesBindIntent(t *testing.T) {
	srv, db := newAuthServer(t)
	p := newStubOIDC(t)
	seedOIDC(t, srv, db, p, auth.PolicyClosed)
	owner := createTestUser(t, srv, "owner", "Sup3rSecret!", store.RoleUser, true)
	cookies, csrf := loginJSON(t, srv, db, "owner", "Sup3rSecret!")

	rec := postJSONWithSession(srv, "/api/v1/settings/identities/bind", map[string]string{}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST bind = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var resp struct {
		AuthorizeURL string `json:"authorize_url"`
	}
	decodeJSON(t, rec, &resp)
	u, err := url.Parse(resp.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorize_url: %v", err)
	}
	q := u.Query()
	if q.Get("state") == "" || q.Get("nonce") == "" || q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorize_url is missing state/nonce/PKCE: %s", resp.AuthorizeURL)
	}
	pending, ok := srv.oidc.TakePending(q.Get("state"))
	if !ok {
		t.Fatal("TakePending(state) = not found, want the pending bind")
	}
	if pending.Intent != auth.PendingIntentBind || pending.UserID != owner.ID {
		t.Fatalf("pending = %+v, want intent=bind user=%d", pending, owner.ID)
	}
}

func TestIdentityBindStartRejectsUnavailableAndMissingCSRF(t *testing.T) {
	srv, db := newAuthServer(t)
	createTestUser(t, srv, "owner", "Sup3rSecret!", store.RoleUser, true)
	cookies, csrf := loginJSON(t, srv, db, "owner", "Sup3rSecret!")

	rec := postJSONWithSession(srv, "/api/v1/settings/identities/bind", map[string]string{}, cookies, csrf)
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST bind while OIDC is disabled = %d, want 409 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeJSON(t, rec, &errResp)
	if errResp.Error.Code != "oidc_unavailable" {
		t.Fatalf("error code = %q, want oidc_unavailable", errResp.Error.Code)
	}
	if rec := postJSONWithSession(srv, "/api/v1/settings/identities/bind", map[string]string{}, cookies, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("POST bind without CSRF = %d, want 403", rec.Code)
	}
}

// bindURL starts a bind and returns state and nonce without consuming the pending entry.
func bindURL(t *testing.T, srv *Server, cookies []*http.Cookie, csrf string) (state, nonce string) {
	t.Helper()
	rec := postJSONWithSession(srv, "/api/v1/settings/identities/bind", map[string]string{}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST bind = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var resp struct {
		AuthorizeURL string `json:"authorize_url"`
	}
	decodeJSON(t, rec, &resp)
	u, err := url.Parse(resp.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorize_url: %v", err)
	}
	return u.Query().Get("state"), u.Query().Get("nonce")
}

func TestIdentityBindCallbackLinksToCaller(t *testing.T) {
	srv, db := newAuthServer(t)
	p := newStubOIDC(t)
	seedOIDC(t, srv, db, p, auth.PolicyClosed)
	owner := createTestUser(t, srv, "owner", "Sup3rSecret!", store.RoleUser, true)
	cookies, csrf := loginJSON(t, srv, db, "owner", "Sup3rSecret!")
	state, nonce := bindURL(t, srv, cookies, csrf)
	p.nonce = nonce

	var before int64
	if err := db.Model(&store.Session{}).Where("user_id = ?", owner.ID).Count(&before).Error; err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	rec := getWithCookies(t, srv, "/auth/oidc/callback?code=stub-code&state="+state, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("bind callback = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if loc := rec.Header().Get("Location"); loc != "/settings/security?identity=linked" {
		t.Fatalf("bind callback Location = %q, want linked", loc)
	}
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == srv.sessions.CookieName() {
			t.Fatal("bind callback unexpectedly issued a session cookie")
		}
	}
	var after int64
	if err := db.Model(&store.Session{}).Where("user_id = ?", owner.ID).Count(&after).Error; err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if after != before {
		t.Fatalf("session count changed %d -> %d during bind", before, after)
	}

	ident, err := store.NewIdentityStore(db).ByProviderSubject(context.Background(), p.srv.URL, p.sub)
	if err != nil {
		t.Fatalf("identity not linked: %v", err)
	}
	if ident.UserID != owner.ID {
		t.Fatalf("identity owner = %d, want %d", ident.UserID, owner.ID)
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionIdentityLink); err != nil || n != 1 {
		t.Fatalf("identity.link audit rows = %d (err %v), want 1", n, err)
	}
}

func TestIdentityBindCallbackRejectsOtherSessionOfSameUser(t *testing.T) {
	srv, db := newAuthServer(t)
	p := newStubOIDC(t)
	seedOIDC(t, srv, db, p, auth.PolicyClosed)
	owner := createTestUser(t, srv, "owner", "Sup3rSecret!", store.RoleUser, true)
	cookiesA, csrfA := loginJSON(t, srv, db, "owner", "Sup3rSecret!")
	cookiesB, _ := loginJSON(t, srv, db, "owner", "Sup3rSecret!")
	state, nonce := bindURL(t, srv, cookiesA, csrfA)
	p.nonce = nonce

	rec := getWithCookies(t, srv, "/auth/oidc/callback?code=stub-code&state="+state, cookiesB)
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || loc != "/settings/security?identity=session" {
		t.Fatalf("second-session callback = (%d, %q), want 303 to identity=session", rec.Code, loc)
	}
	if rows := identityRows(t, db, owner.ID); len(rows) != 0 {
		t.Fatalf("second session wrote %d identities, want 0", len(rows))
	}
}

func TestIdentityBindCallbackRejectsOtherSession(t *testing.T) {
	srv, db := newAuthServer(t)
	p := newStubOIDC(t)
	seedOIDC(t, srv, db, p, auth.PolicyClosed)
	owner := createTestUser(t, srv, "owner", "Sup3rSecret!", store.RoleUser, true)
	other := createTestUser(t, srv, "other", "Sup3rSecret!", store.RoleUser, true)
	ownerCookies, ownerCSRF := loginJSON(t, srv, db, "owner", "Sup3rSecret!")
	otherCookies, _ := loginJSON(t, srv, db, "other", "Sup3rSecret!")
	state, nonce := bindURL(t, srv, ownerCookies, ownerCSRF)
	p.nonce = nonce

	rec := getWithCookies(t, srv, "/auth/oidc/callback?code=stub-code&state="+state, otherCookies)
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || loc != "/settings/security?identity=session" {
		t.Fatalf("mismatched-session callback = (%d, %q), want 303 to identity=session", rec.Code, loc)
	}
	if rows := identityRows(t, db, other.ID); len(rows) != 0 {
		t.Fatalf("mismatched session wrote %d identities for the session user, want 0", len(rows))
	}
	if rows := identityRows(t, db, owner.ID); len(rows) != 0 {
		t.Fatalf("mismatched session wrote identities for the pending user, want 0")
	}
}

func TestIdentityBindCallbackSubjectTaken(t *testing.T) {
	srv, db := newAuthServer(t)
	p := newStubOIDC(t)
	seedOIDC(t, srv, db, p, auth.PolicyClosed)
	owner := createTestUser(t, srv, "owner", "Sup3rSecret!", store.RoleUser, true)
	other := createTestUser(t, srv, "other", "Sup3rSecret!", store.RoleUser, true)
	if err := store.NewIdentityStore(db).Create(context.Background(), &store.Identity{
		UserID: other.ID, Provider: p.srv.URL, Subject: p.sub,
	}); err != nil {
		t.Fatalf("seed taken identity: %v", err)
	}
	cookies, csrf := loginJSON(t, srv, db, "owner", "Sup3rSecret!")
	state, nonce := bindURL(t, srv, cookies, csrf)
	p.nonce = nonce

	rec := getWithCookies(t, srv, "/auth/oidc/callback?code=stub-code&state="+state, cookies)
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || loc != "/settings/security?identity=taken" {
		t.Fatalf("taken-subject callback = (%d, %q), want 303 to identity=taken", rec.Code, loc)
	}
	if rows := identityRows(t, db, owner.ID); len(rows) != 0 {
		t.Fatalf("taken subject created %d identities for the caller, want 0", len(rows))
	}
	ident, err := store.NewIdentityStore(db).ByProviderSubject(context.Background(), p.srv.URL, p.sub)
	if err != nil || ident.UserID != other.ID {
		t.Fatalf("taken identity moved: %+v, err=%v", ident, err)
	}
}

func TestIdentityRoutesRequireSessionAndCSRF(t *testing.T) {
	srv, db := newAuthServer(t)
	owner := createTestUser(t, srv, "owner", "Sup3rSecret!", store.RoleUser, true)
	ident := &store.Identity{UserID: owner.ID, Provider: "https://issuer.example.com", Subject: "sub-owner"}
	if err := store.NewIdentityStore(db).Create(context.Background(), ident); err != nil {
		t.Fatalf("seed identity: %v", err)
	}
	cookies, _ := loginJSON(t, srv, db, "owner", "Sup3rSecret!")

	if rec := getWithCookies(t, srv, "/api/v1/settings/identities", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous list = %d, want 401", rec.Code)
	}
	if rec := postJSONWithSession(srv, "/api/v1/settings/identities/bind", map[string]string{}, nil, ""); rec.Code != http.StatusForbidden {
		t.Errorf("anonymous bind = %d, want 403", rec.Code)
	}
	if rec := jsonRequest(t, srv, http.MethodDelete, "/api/v1/settings/identities/"+ident.PublicID, "", nil, ""); rec.Code != http.StatusForbidden {
		t.Errorf("anonymous unlink = %d, want 403", rec.Code)
	}
	if rec := jsonRequest(t, srv, http.MethodDelete, "/api/v1/settings/identities/"+ident.PublicID, "", cookies, ""); rec.Code != http.StatusForbidden {
		t.Errorf("unlink without CSRF = %d, want 403", rec.Code)
	}
	if _, err := store.NewIdentityStore(db).ByPublicID(context.Background(), ident.PublicID); err != nil {
		t.Fatalf("identity removed by a rejected unlink: %v", err)
	}
}

func TestIdentityBindCallbackFailures(t *testing.T) {
	srv, db := newAuthServer(t)
	p := newStubOIDC(t)
	seedOIDC(t, srv, db, p, auth.PolicyClosed)
	owner := createTestUser(t, srv, "owner", "Sup3rSecret!", store.RoleUser, true)
	cookies, csrf := loginJSON(t, srv, db, "owner", "Sup3rSecret!")

	// No session: the pending bind belongs to a session, so even the right user id is not enough.
	state, _ := bindURL(t, srv, cookies, csrf)
	rec := getWithCookies(t, srv, "/auth/oidc/callback?code=stub-code&state="+state, nil)
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || loc != "/settings/security?identity=session" {
		t.Fatalf("no-session bind callback = (%d, %q), want identity=session", rec.Code, loc)
	}

	// No code: the state is valid and the session matches, but there is nothing to exchange.
	state, nonce := bindURL(t, srv, cookies, csrf)
	p.nonce = nonce
	rec = getWithCookies(t, srv, "/auth/oidc/callback?state="+state, cookies)
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || loc != "/settings/security?identity=failed" {
		t.Fatalf("no-code bind callback = (%d, %q), want identity=failed", rec.Code, loc)
	}

	// Wrong nonce: the ID token is rejected before any link is written.
	state, _ = bindURL(t, srv, cookies, csrf)
	p.nonce = "wrong-nonce"
	rec = getWithCookies(t, srv, "/auth/oidc/callback?code=stub-code&state="+state, cookies)
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || loc != "/settings/security?identity=failed" {
		t.Fatalf("wrong-nonce bind callback = (%d, %q), want identity=failed", rec.Code, loc)
	}

	if rows := identityRows(t, db, owner.ID); len(rows) != 0 {
		t.Fatalf("failed bind callbacks wrote %d identities, want 0", len(rows))
	}
}

func TestIdentityUnlink(t *testing.T) {
	ctx := context.Background()
	srv, db := newAuthServer(t)
	owner := createTestUser(t, srv, "owner", "Sup3rSecret!", store.RoleUser, true)
	other := createTestUser(t, srv, "other", "Sup3rSecret!", store.RoleUser, true)
	identities := store.NewIdentityStore(db)
	own := &store.Identity{UserID: owner.ID, Provider: "https://issuer.example.com", Subject: "sub-owner"}
	foreign := &store.Identity{UserID: other.ID, Provider: "https://issuer.example.com", Subject: "sub-other"}
	for _, ident := range []*store.Identity{own, foreign} {
		if err := identities.Create(ctx, ident); err != nil {
			t.Fatalf("seed identity: %v", err)
		}
	}
	cookies, csrf := loginJSON(t, srv, db, "owner", "Sup3rSecret!")

	if rec := jsonRequest(t, srv, http.MethodDelete, "/api/v1/settings/identities/"+foreign.PublicID, "", cookies, csrf); rec.Code != http.StatusNotFound {
		t.Fatalf("unlink another user's identity = %d, want 404 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if _, err := identities.ByPublicID(ctx, foreign.PublicID); err != nil {
		t.Fatalf("foreign identity was removed: %v", err)
	}

	if rec := jsonRequest(t, srv, http.MethodDelete, "/api/v1/settings/identities/"+own.PublicID, "", cookies, csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink own identity = %d, want 204 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if _, err := identities.ByPublicID(ctx, own.PublicID); err == nil {
		t.Fatal("own identity still exists after unlink")
	}
	if n, err := store.NewAuditStore(db).CountByAction(ctx, store.ActionIdentityUnlink); err != nil || n != 1 {
		t.Fatalf("identity.unlink audit rows = %d (err %v), want 1", n, err)
	}
}

func TestIdentityBindCallbackProviderError(t *testing.T) {
	srv, db := newAuthServer(t)
	p := newStubOIDC(t)
	seedOIDC(t, srv, db, p, auth.PolicyClosed)
	owner := createTestUser(t, srv, "owner", "Sup3rSecret!", store.RoleUser, true)
	cookies, csrf := loginJSON(t, srv, db, "owner", "Sup3rSecret!")
	state, _ := bindURL(t, srv, cookies, csrf)

	rec := getWithCookies(t, srv, "/auth/oidc/callback?error=access_denied&state="+state, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("cancelled bind callback = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/settings/security?identity=failed" {
		t.Fatalf("cancelled bind Location = %q, want identity=failed", loc)
	}
	if _, ok := srv.oidc.TakePending(state); ok {
		t.Fatal("cancelled bind left the pending state in the table")
	}
	if rows := identityRows(t, db, owner.ID); len(rows) != 0 {
		t.Fatalf("cancelled bind wrote %d identities, want 0", len(rows))
	}
}

func TestOIDCLoginProviderErrorStillGoesToLogin(t *testing.T) {
	srv, db := newAuthServer(t)
	p := newStubOIDC(t)
	seedOIDC(t, srv, db, p, auth.PolicyClosed)
	state, _ := startOIDCLogin(t, srv)

	rec := getWithCookies(t, srv, "/auth/oidc/callback?error=access_denied&state="+state, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("cancelled login callback = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("cancelled login Location = %q, want /login", loc)
	}
	if _, ok := srv.oidc.TakePending(state); ok {
		t.Fatal("cancelled login left the pending state in the table")
	}
}

func TestIdentityUnlinkLastCredential(t *testing.T) {
	ctx := context.Background()
	srv, db := newAuthServer(t)
	identities := store.NewIdentityStore(db)

	// Passwordless account with exactly one identity: the unlink must be refused.
	passwordless, err := srv.accounts.CreateOIDCUser(ctx, auth.CreateUserInput{
		Username: "oidc-user", Email: "oidc-user@example.com",
	})
	if err != nil {
		t.Fatalf("CreateOIDCUser: %v", err)
	}
	only := &store.Identity{UserID: passwordless.ID, Provider: "https://issuer.example.com", Subject: "sub-only"}
	if err := identities.Create(ctx, only); err != nil {
		t.Fatalf("seed identity: %v", err)
	}
	cookies, csrf := sessionFor(t, srv, passwordless.ID)
	rec := jsonRequest(t, srv, http.MethodDelete, "/api/v1/settings/identities/"+only.PublicID, "", cookies, csrf)
	if rec.Code != http.StatusConflict {
		t.Fatalf("unlink last credential = %d, want 409 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if _, err := identities.ByPublicID(ctx, only.PublicID); err != nil {
		t.Fatalf("last identity was removed: %v", err)
	}

	// The same account with a password may unlink its last identity.
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return srv.accounts.SetInitialPasswordTx(ctx, tx, passwordless.ID, "Sup3rSecret!", "")
	}); err != nil {
		t.Fatalf("SetInitialPasswordTx: %v", err)
	}
	cookies, csrf = loginJSON(t, srv, db, "oidc-user", "Sup3rSecret!")
	rec = jsonRequest(t, srv, http.MethodDelete, "/api/v1/settings/identities/"+only.PublicID, "", cookies, csrf)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unlink last identity with a password = %d, want 204 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
}
