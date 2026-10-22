package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// listDeckRolesViaHTTP calls GET /api/v1/decks with a key and returns a deck public id -> role map.
func listDeckRolesViaHTTP(t *testing.T, env *testEnv, key string) map[string]string {
	t.Helper()
	status, raw := doJSON(t, env.router(), http.MethodGet, "/api/v1/decks", key, "")
	if status != http.StatusOK {
		t.Fatalf("GET /decks status = %d, want 200 (body %s)", status, raw)
	}
	var body struct {
		Decks []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"decks"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode decks: %v (%s)", err, raw)
	}
	out := make(map[string]string, len(body.Decks))
	for _, d := range body.Decks {
		out[d.ID] = d.Role
	}
	return out
}

// leaveDeckViaHTTP calls DELETE /api/v1/decks/:id/membership with a key (:id is the deck's external id).
func leaveDeckViaHTTP(t *testing.T, env *testEnv, key string, deckPublicID string) (int, []byte) {
	t.Helper()
	return doJSON(t, env.router(), http.MethodDelete, fmt.Sprintf("/api/v1/decks/%s/membership", deckPublicID), key, "")
}

// TestListDecksReportsCallerRole asserts the list carries the caller's explicit relation for every deck:
// owned -> owner, explicitly granted -> the granted role; a deck someone else has not granted me never enters the list.
//
// The role is necessarily one of the two (owner or a granted role): there is no deck that is "visible but has no role",
// so the UI needs no branch for that case.
func TestListDecksReportsCallerRole(t *testing.T) {
	env := newTestEnv(t, 600, 600)
	owner := seedUser(t, env.db, "role_owner", store.RoleUser)
	viewer := seedUser(t, env.db, "role_viewer", store.RoleUser)

	ownDeck := seedDeck(t, env.db, viewer.ID)
	grantedDeck := seedDeck(t, env.db, owner.ID)
	foreignDeck := seedDeck(t, env.db, owner.ID)
	if err := store.NewGrantStore(env.db).Grant(context.Background(), grantedDeck.ID, viewer.ID, store.RoleEditor, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant viewer: %v", err)
	}

	key := seedKey(t, env.keys, viewer.ID, []string{store.ScopeRead}, nil)
	roles := listDeckRolesViaHTTP(t, env, key.Plaintext)

	if roles[ownDeck.PublicID] != store.RoleOwner {
		t.Errorf("own deck role = %q, want %q", roles[ownDeck.PublicID], store.RoleOwner)
	}
	if roles[grantedDeck.PublicID] != store.RoleEditor {
		t.Errorf("granted deck role = %q, want %q", roles[grantedDeck.PublicID], store.RoleEditor)
	}
	if _, ok := roles[foreignDeck.PublicID]; ok {
		t.Errorf("another user's ungranted deck %s appeared in the list: %v", foreignDeck.PublicID, roles)
	}
	if len(roles) != 2 {
		t.Errorf("listed roles = %v, want exactly the owned and the granted deck", roles)
	}
}

// TestLeaveDeckRemovesOwnGrant is the core acceptance: a share recipient can leave - the grant row is deleted, the audit records it,
// and the very next access is rejected immediately (revocation applies at once).
func TestLeaveDeckRemovesOwnGrant(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "leave_owner", store.RoleUser)
	member := seedUser(t, env.db, "leave_member", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	ctx := context.Background()
	grants := store.NewGrantStore(env.db)
	if err := grants.Grant(ctx, deck.ID, member.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant member: %v", err)
	}
	key := seedKey(t, env.keys, member.ID, []string{store.ScopeWrite}, nil)

	status, raw := leaveDeckViaHTTP(t, env, key.Plaintext, deck.PublicID)
	if status != http.StatusOK {
		t.Fatalf("leave status = %d, want 200 (body %s)", status, raw)
	}
	if role, err := grants.Role(ctx, deck.ID, member.ID); err != nil || role != "" {
		t.Fatalf("grant after leave = (%q,%v), want (\"\",nil)", role, err)
	}
	// Leaving takes effect at once: the same member's next access is rejected.
	if _, err := env.api.RequireDeckRole(ctx, member.ID, deck.ID, store.RoleReader); err == nil {
		t.Fatalf("member still has access after leaving")
	}
	// The audit records it: one deck.revoke.
	if n, err := store.NewAuditStore(env.db).CountByAction(ctx, store.ActionDeckRevoke); err != nil || n != 1 {
		t.Fatalf("deck.revoke audit rows = %d (err %v), want 1", n, err)
	}
}

// TestLeaveDeckRejectsOwner asserts the owner cannot "leave" their own deck: for an owned deck only deleting exists,
// so leaving must be rejected, lest the irreversible delete be confused with the reversible leave.
func TestLeaveDeckRejectsOwner(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "leave_own_owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)

	err := env.api.LeaveDeck(context.Background(), owner, deck.ID, nil)
	status, code := serviceCode(t, err)
	if status != http.StatusBadRequest || code != CodeInvalidRequest {
		t.Fatalf("owner LeaveDeck = (%d,%q), want (400,%q)", status, code, CodeInvalidRequest)
	}
}

// TestLeaveDeckRejectsNonMember asserts a user with no grant row has no membership to leave:
// it returns not_found, and they could not open that deck anyway (403) - "no membership" does not equal "can read".
func TestLeaveDeckRejectsNonMember(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "leave_nm_owner", store.RoleUser)
	stranger := seedUser(t, env.db, "leave_nm_stranger", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	ctx := context.Background()

	err := env.api.LeaveDeck(ctx, stranger, deck.ID, nil)
	status, code := serviceCode(t, err)
	if status != http.StatusNotFound || code != CodeNotFound {
		t.Fatalf("stranger LeaveDeck = (%d,%q), want (404,%q)", status, code, CodeNotFound)
	}
	if _, err := env.api.RequireDeckRole(ctx, stranger.ID, deck.ID, store.RoleReader); err == nil {
		t.Fatalf("stranger could read a deck he was never granted")
	}
}

// TestLeaveDeckRequiresWriteScope asserts a read-only key hitting the leave endpoint is stopped by the scope gate, with the grant row left untouched.
func TestLeaveDeckRequiresWriteScope(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "leave_scope_owner", store.RoleUser)
	member := seedUser(t, env.db, "leave_scope_member", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	ctx := context.Background()
	if err := store.NewGrantStore(env.db).Grant(ctx, deck.ID, member.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant member: %v", err)
	}
	key := seedKey(t, env.keys, member.ID, []string{store.ScopeRead}, nil)

	status, raw := leaveDeckViaHTTP(t, env, key.Plaintext, deck.PublicID)
	if status != http.StatusForbidden {
		t.Fatalf("read-scope leave status = %d, want 403 (body %s)", status, raw)
	}
	if role, err := store.NewGrantStore(env.db).Role(ctx, deck.ID, member.ID); err != nil || role != store.RoleReader {
		t.Fatalf("grant after scope-denied leave = (%q,%v), want (%q,nil)", role, err, store.RoleReader)
	}
}
