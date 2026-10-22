package web

import (
	"context"
	"net/http"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// createUserAndLogin creates an ordinary user on the same test database and logs in, returning their id, session cookie and CSRF token.
func createUserAndLogin(t *testing.T, srv *Server, db *gorm.DB, username string) (uint64, []*http.Cookie, string) {
	t.Helper()
	accounts, err := auth.NewAccountService(store.NewUserStore(db), store.NewSessionStore(db), store.NewAPIKeyStore(db),
		auth.NewPasswordHasher(auth.Params{Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32}))
	if err != nil {
		t.Fatalf("NewAccountService() error = %v", err)
	}
	u, err := accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: username, Email: username + "@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser(%q) error = %v", username, err)
	}
	// Login goes through the SPA's same-origin JSON endpoint: a failure is Fatal, success returns the session cookie and the session-bound CSRF token.
	cookies, csrf := loginJSON(t, srv, db, username, "Sup3rSecret!")
	return u.ID, cookies, csrf
}

// grantsJSONPath is the JSON path for sharing grants.
func grantsJSONPath(deckPublicID string) string {
	return "/api/v1/decks/" + deckPublicID + "/sharing/grants"
}

// TestSharingGrantInvitesThenAcceptanceGivesAccess is the main acceptance, and its semantics are the **consent model**:
// the owner sharing only sends an invite and produces no grant; the invitee gets access only after accepting;
// after revocation the other side's next request is refused immediately. Each step writes one audit line.
func TestSharingGrantInvitesThenAcceptanceGivesAccess(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Shared deck")
	deckPath := "/decks/" + deck.PublicID
	user2ID, u2Cookies, u2CSRF := createUserAndLogin(t, srv, db, "reader2")
	u2, err := store.NewUserStore(db).ByID(context.Background(), user2ID)
	if err != nil {
		t.Fatalf("load reader2: %v", err)
	}

	// Before the grant: no access, and GET on the list page is refused with 403.
	if rec := getWithCookies(t, srv, deckPath+"/notes", u2Cookies); rec.Code != http.StatusForbidden {
		t.Fatalf("before grant: GET notes status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}

	// The owner shares: this is only an **invite**, not a grant.
	rec := jsonRequest(t, srv, http.MethodPost, grantsJSONPath(deck.PublicID),
		`{"username":"reader2","role":"reader"}`, ownerCookies, ownerCSRF)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST grant status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	// The key negative case: before accepting, the other side **cannot** access — that is what separates the consent model from the old behavior.
	if rec := getWithCookies(t, srv, deckPath+"/notes", u2Cookies); rec.Code != http.StatusForbidden {
		t.Fatalf("before acceptance: GET notes status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}

	// The invitee accepts: only this step writes the grant.
	if rec := jsonRequest(t, srv, http.MethodPost,
		"/api/v1/sharing/invites/"+deck.PublicID+"/accept", "", u2Cookies, u2CSRF); rec.Code != http.StatusOK {
		t.Fatalf("accept invite status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := getWithCookies(t, srv, deckPath+"/notes", u2Cookies); rec.Code != http.StatusOK {
		t.Fatalf("after acceptance: GET notes status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// Role change reader -> editor: writes deck.role_change, and the other side can still access.
	rec = jsonRequest(t, srv, http.MethodPatch, grantsJSONPath(deck.PublicID)+"/"+u2.PublicID,
		`{"role":"editor"}`, ownerCookies, ownerCSRF)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH role status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := getWithCookies(t, srv, deckPath+"/notes", u2Cookies); rec.Code != http.StatusOK {
		t.Fatalf("after role change: GET notes status = %d, want 200", rec.Code)
	}

	// Revocation: the other side's next request is refused immediately.
	rec = jsonRequest(t, srv, http.MethodDelete, grantsJSONPath(deck.PublicID)+"/"+u2.PublicID,
		"", ownerCookies, ownerCSRF)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE grant status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := getWithCookies(t, srv, deckPath+"/notes", u2Cookies); rec.Code != http.StatusForbidden {
		t.Fatalf("after revoke: GET notes status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}

	// Audit: 1 invite, 1 accept, 1 role change, 1 revoke.
	audits := store.NewAuditStore(db)
	for _, c := range []struct {
		action string
		want   int64
	}{
		{store.ActionDeckShareInvite, 1},
		{store.ActionDeckShareAccept, 1},
		{store.ActionDeckRoleChange, 1},
		{store.ActionDeckRevoke, 1},
	} {
		n, err := audits.CountByAction(context.Background(), c.action)
		if err != nil {
			t.Fatalf("count audit %s: %v", c.action, err)
		}
		if n != c.want {
			t.Errorf("audit rows for %s = %d, want %d", c.action, n, c.want)
		}
	}
}

// TestSharingPageRejectsNonOwner covers the negative cases: a non-owner cannot open the sharing page, and the write endpoints are refused with nothing written.
func TestSharingPageRejectsNonOwner(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Owner only")
	deckPath := "/decks/" + deck.PublicID
	// Create an ordinary user and grant editor: they are still not the owner and cannot manage grants.
	user2ID, u2Cookies, u2CSRF := createUserAndLogin(t, srv, db, "editor2")
	if rec := jsonRequest(t, srv, http.MethodPost, grantsJSONPath(deck.PublicID),
		`{"username":"editor2","role":"editor"}`, ownerCookies, ownerCSRF); rec.Code != http.StatusOK {
		t.Fatalf("owner grant editor status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	// Under the consent model it must be accepted first for the editor to really be an editor (what this case tests is "someone with access is still not the owner").
	if rec := jsonRequest(t, srv, http.MethodPost,
		"/api/v1/sharing/invites/"+deck.PublicID+"/accept", "", u2Cookies, u2CSRF); rec.Code != http.StatusOK {
		t.Fatalf("editor accept invite status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// The editor opens the sharing page -> 403.
	if rec := getWithCookies(t, srv, deckPath+"/sharing", u2Cookies); rec.Code != http.StatusForbidden {
		t.Fatalf("editor GET sharing status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	// The editor submits a grant -> 403, and no grant row is added.
	before, err := store.NewGrantStore(db).ListByDeck(context.Background(), deck.ID)
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if rec := jsonRequest(t, srv, http.MethodPost, grantsJSONPath(deck.PublicID),
		`{"username":"editor2","role":"reader"}`, u2Cookies, u2CSRF); rec.Code != http.StatusForbidden {
		t.Fatalf("editor POST grant status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	after, err := store.NewGrantStore(db).ListByDeck(context.Background(), deck.ID)
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("grant rows changed by non-owner: before=%d after=%d", len(before), len(after))
	}
	role, err := store.NewGrantStore(db).Role(context.Background(), deck.ID, user2ID)
	if err != nil {
		t.Fatalf("read editor role: %v", err)
	}
	if role != store.RoleEditor {
		t.Errorf("editor role = %q, want %q (unchanged)", role, store.RoleEditor)
	}
}

// TestSharingGrantRequiresCSRF is a mandatory negative case: a grant write without a CSRF token is refused.
func TestSharingGrantRequiresCSRF(t *testing.T) {
	srv, db, ownerID, ownerCookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "CSRF deck")
	createUserAndLogin(t, srv, db, "csrf_target")
	rec := jsonRequest(t, srv, http.MethodPost, grantsJSONPath(deck.PublicID),
		`{"username":"csrf_target","role":"reader"}`, ownerCookies, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST grant without CSRF status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	grants, err := store.NewGrantStore(db).ListByDeck(context.Background(), deck.ID)
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if len(grants) != 0 {
		t.Errorf("grant created despite missing CSRF: %d row(s)", len(grants))
	}
}

// TestSharingMutationsKeepCSRF asserts the shell switch did not loosen CSRF: a SPA JSON write
// (/api/v1/decks/:id/sharing/grants) without a token is 403, and only a correct token passes.
func TestSharingMutationsKeepCSRF(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "CSRF sharing deck")
	_, _, _ = createUserAndLogin(t, srv, db, "csrf-target")

	apiPath := "/api/v1/decks/" + deck.PublicID + "/sharing/grants"
	if rec := jsonRequest(t, srv, http.MethodPost, apiPath, `{"username":"csrf-target","role":"editor"}`, ownerCookies, ""); rec.Code != http.StatusForbidden {
		t.Errorf("SPA grant without CSRF = %d, want 403", rec.Code)
	}
	if rec := jsonRequest(t, srv, http.MethodPost, apiPath, `{"username":"csrf-target","role":"editor"}`, ownerCookies, ownerCSRF); rec.Code != http.StatusOK {
		t.Errorf("SPA grant with CSRF = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
}
