package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// This file is the acceptance for the share consent model: sending an invite does not grant, only acceptance takes effect, a rejection leaves no trace,
// and the receiving policy (anyone / whitelist / nobody) blocks **before the invite is sent**.

func decodeShareInvites(t *testing.T, body []byte) shareInvitesResponse {
	t.Helper()
	var out shareInvitesResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode share invites: %v", err)
	}
	return out
}

// TestShareInviteRequiresAcceptance is the heart of the consent model: after the owner shares, the invitee has **no** grant yet,
// not until they accept it themselves.
func TestShareInviteRequiresAcceptance(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Consent deck")
	invitee := newGrantee(t, srv, "invitee", "invitee@example.com")
	ctx := context.Background()
	grants := store.NewGrantStore(db)
	invites := store.NewDeckShareInviteStore(db)
	base := "/api/v1/decks/" + deck.PublicID + "/sharing/grants"

	rec := jsonRequest(t, srv, http.MethodPost, base,
		`{"user_id":"`+invitee.PublicID+`","role":"reader"}`, cookies, csrf)
	if rec.Code >= 300 {
		t.Fatalf("POST grant = %d, want success (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	// The point: **no** grant appeared, only one pending invite.
	if role, err := grants.Role(ctx, deck.ID, invitee.ID); err != nil || role != "" {
		t.Fatalf("grant role = %q (err %v), want no grant before acceptance", role, err)
	}
	if _, err := invites.ByDeckAndUser(ctx, deck.ID, invitee.ID); err != nil {
		t.Fatalf("invite missing after the share: %v", err)
	}

	// The owner shares the same person with the same role again: it is still that one invite, not stacked.
	rec = jsonRequest(t, srv, http.MethodPost, base,
		`{"user_id":"`+invitee.PublicID+`","role":"reader"}`, cookies, csrf)
	if rec.Code >= 300 {
		t.Fatalf("second POST grant = %d, want success", rec.Code)
	}
	list, err := invites.ListForUser(ctx, invitee.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("invites for the recipient = %d (err %v), want exactly 1", len(list), err)
	}
	if list[0].DeckName != "Consent deck" {
		t.Errorf("invite deck name = %q, want the deck name for display", list[0].DeckName)
	}
}

// TestShareInviteAcceptAndReject covers the two exits, accept and reject.
func TestShareInviteAcceptAndReject(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Consent deck 2")
	// The invitee logs in themselves to accept/reject, so the fixture is "create account + log in".
	inviteeID, inviteeCookies, inviteeCSRF := createUserAndLogin(t, srv, db, "invitee2")
	ctx := context.Background()
	grants := store.NewGrantStore(db)
	invites := store.NewDeckShareInviteStore(db)

	// The owner shares twice: one will be accepted, one will be rejected.
	other := seedReviewDeck(t, db, ownerID, "Consent deck 3")
	for _, d := range []*store.Deck{deck, other} {
		rec := jsonRequest(t, srv, http.MethodPost, "/api/v1/decks/"+d.PublicID+"/sharing/grants",
			`{"user_id":"`+userPublicID(t, db, inviteeID)+`","role":"editor"}`, cookies, csrf)
		if rec.Code >= 300 {
			t.Fatalf("POST grant = %d, want success", rec.Code)
		}
	}

	// Both are visible in the list.
	rec := getJSON(t, srv, "/api/v1/sharing/invites", inviteeCookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET invites = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	pending := decodeShareInvites(t, rec.Body.Bytes())
	if len(pending.Invites) != 2 {
		t.Fatalf("pending invites = %d, want 2", len(pending.Invites))
	}
	if pending.Policy != store.ShareAcceptAnyone {
		t.Errorf("policy = %q, want anyone by default", pending.Policy)
	}

	// Accepting the first: the grant appears, the invite is gone.
	rec = jsonRequest(t, srv, http.MethodPost, "/api/v1/sharing/invites/"+deck.PublicID+"/accept", "", inviteeCookies, inviteeCSRF)
	if rec.Code != http.StatusOK {
		t.Fatalf("accept = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if role, err := grants.Role(ctx, deck.ID, inviteeID); err != nil || role != store.RoleEditor {
		t.Fatalf("grant role after accept = %q (err %v), want editor", role, err)
	}
	if _, err := invites.ByDeckAndUser(ctx, deck.ID, inviteeID); err == nil {
		t.Error("invite should be gone after acceptance")
	}

	// Rejecting the second: the invite is gone and no grant ever appeared.
	rec = jsonRequest(t, srv, http.MethodPost, "/api/v1/sharing/invites/"+other.PublicID+"/reject", "", inviteeCookies, inviteeCSRF)
	if rec.Code != http.StatusOK {
		t.Fatalf("reject = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if role, err := grants.Role(ctx, other.ID, inviteeID); err != nil || role != "" {
		t.Errorf("grant role after reject = %q (err %v), want none", role, err)
	}
	// Both accept and reject write an audit entry.
	for _, action := range []string{store.ActionDeckShareAccept, store.ActionDeckShareReject} {
		if n, err := store.NewAuditStore(db).CountByAction(ctx, action); err != nil || n != 1 {
			t.Errorf("audit rows for %s = %d (err %v), want 1", action, n, err)
		}
	}
}

// TestSharePolicyRefusesInvites covers the refuse policies: both nobody and whitelist block **before the invite is sent**.
func TestSharePolicyRefusesInvites(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Refused deck")
	invitee := newGrantee(t, srv, "refuser", "refuser@example.com")
	ctx := context.Background()
	policy := store.NewSharePolicyStore(db)
	invites := store.NewDeckShareInviteStore(db)
	path := "/api/v1/decks/" + deck.PublicID + "/sharing/grants"
	body := `{"user_id":"` + invitee.PublicID + `","role":"reader"}`

	// nobody: rejected outright, and no invite is left behind.
	if err := policy.SetPolicy(ctx, invitee.ID, store.ShareAcceptNobody); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	rec := jsonRequest(t, srv, http.MethodPost, path, body, cookies, csrf)
	if rec.Code != http.StatusConflict {
		t.Fatalf("grant to a refusing recipient = %d, want 409 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if _, err := invites.ByDeckAndUser(ctx, deck.ID, invitee.ID); err == nil {
		t.Error("no invite should be created for a recipient who refuses shares")
	}

	// whitelist: not on the list is rejected the same way, and it is let through once added to the list.
	if err := policy.SetPolicy(ctx, invitee.ID, store.ShareAcceptWhitelist); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	if rec := jsonRequest(t, srv, http.MethodPost, path, body, cookies, csrf); rec.Code != http.StatusConflict {
		t.Fatalf("grant with an empty whitelist = %d, want 409", rec.Code)
	}
	if err := policy.Allow(ctx, invitee.ID, ownerID); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if rec := jsonRequest(t, srv, http.MethodPost, path, body, cookies, csrf); rec.Code >= 300 {
		t.Fatalf("grant from a whitelisted owner = %d, want success (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	// The default (anyone) lets it through.
	other := newGrantee(t, srv, "open-user", "open-user@example.com")
	if rec := jsonRequest(t, srv, http.MethodPost, path,
		`{"user_id":"`+other.PublicID+`","role":"reader"}`, cookies, csrf); rec.Code >= 300 {
		t.Fatalf("grant to a default-policy recipient = %d, want success", rec.Code)
	}
}

// TestSharePolicySaveEndpoint covers the policy save endpoint: validating illegal values, writing an audit entry, adding and removing allow-list entries.
func TestSharePolicySaveEndpoint(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	other := newGrantee(t, srv, "allowee", "allowee@example.com")

	rec := jsonRequest(t, srv, http.MethodPut, "/api/v1/settings/share-policy",
		`{"policy":"sometimes"}`, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT invalid policy = %d, want 400", rec.Code)
	}
	rec = jsonRequest(t, srv, http.MethodPut, "/api/v1/settings/share-policy",
		`{"policy":"whitelist","allow":["`+other.PublicID+`"]}`, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT policy = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	allow, err := store.NewSharePolicyStore(db).AllowList(context.Background(), ownerID)
	if err != nil || len(allow) != 1 || allow[0] != other.ID {
		t.Fatalf("allow list = %v (err %v), want [%d]", allow, err, other.ID)
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionSharePolicyUpdate); err != nil || n != 1 {
		t.Errorf("audit rows = %d (err %v), want 1", n, err)
	}
	// The username form (what the UI uses): known names go on the list, an unknown name rejects the whole request and changes no state.
	rec = jsonRequest(t, srv, http.MethodPut, "/api/v1/settings/share-policy",
		`{"allow_usernames":["allowee"]}`, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT allow_usernames = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var after map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode policy response: %v", err)
	}
	// The server truth read back in the response: the allow list carries usernames, so the UI need not query again.
	if !strings.Contains(string(after["allow_list"]), "allowee") {
		t.Errorf("allow_list = %s, want the resolved username", after["allow_list"])
	}
	rec = jsonRequest(t, srv, http.MethodPut, "/api/v1/settings/share-policy",
		`{"allow_usernames":["nobody-by-this-name"]}`, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT unknown username = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// Removed from the allow list.
	rec = jsonRequest(t, srv, http.MethodPut, "/api/v1/settings/share-policy",
		`{"revoke":["`+other.PublicID+`"]}`, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT revoke = %d, want 200", rec.Code)
	}
	allow, err = store.NewSharePolicyStore(db).AllowList(context.Background(), ownerID)
	if err != nil || len(allow) != 0 {
		t.Errorf("allow list after revoke = %v (err %v), want empty", allow, err)
	}
}

// TestShareInviteListEmptyIsArray guards the empty case: a user with no pending invite gets "invites":[] rather than null,
// because the SPA deck list reads invites.length and a null crashed the page.
func TestShareInviteListEmptyIsArray(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	rec := getJSON(t, srv, "/api/v1/sharing/invites", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET invites = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if body := rec.Body.String(); !strings.Contains(body, `"invites":[]`) {
		t.Errorf("GET invites body = %s, want an empty invites array", snippet(body))
	}
}
