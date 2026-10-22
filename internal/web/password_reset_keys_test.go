package web

import (
	"context"
	"net/http"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is acceptance testing: a password reset (forgot password, admin reset) must revoke all of that user's
// API keys in the same transaction; a user changing their own password does not revoke them, keeping keys on an independent lifecycle.
//
// The assertions come in three layers: revoked_at is written in the database, the old key gets 401 from the real /api/v1 chain, and the old session is invalidated at the same time.
// The negative control (TestSelfPasswordChangeKeepsAPIKeys) pins down the boundary "changing your own password does not revoke".

// seedAPIKey writes a read-scope key for a user directly and returns the creation result (including the one-time plaintext).
func seedAPIKey(t *testing.T, db *gorm.DB, userID uint64, name string) *store.CreatedAPIKey {
	t.Helper()
	created, err := store.NewAPIKeyStore(db).Create(context.Background(), store.CreateAPIKeyParams{
		UserID: userID, Name: name, Scopes: []string{store.ScopeRead},
	})
	if err != nil {
		t.Fatalf("Create() api key %q error = %v", name, err)
	}
	return created
}

// assertAllKeysRevoked asserts every key for a user in the database has revoked_at written.
func assertAllKeysRevoked(t *testing.T, db *gorm.DB, userID uint64) {
	t.Helper()
	keys, err := store.NewAPIKeyStore(db).ListByUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListByUser(%d) error = %v", userID, err)
	}
	if len(keys) == 0 {
		t.Fatalf("user %d has no api keys; nothing to assert", userID)
	}
	for _, k := range keys {
		if k.RevokedAt == nil {
			t.Errorf("api key %d (%s) for user %d is not revoked", k.ID, k.Name, userID)
		}
	}
}

// TestForgotPasswordResetRevokesAPIKeys covers the full "forgot password" flow:
// the user first holds two working keys → start a reset + reset with the real token → both keys are immediately 401 and invalid in the database,
// while the old session is still revoked (regression protection: session behaviour must not be dropped in favour of keys).
func TestForgotPasswordResetRevokesAPIKeys(t *testing.T) {
	ts := newSecurityServer(t, true)
	rest := newKeysAPI(t, ts.db)
	k1 := seedAPIKey(t, ts.db, ts.ownerID, "laptop")
	k2 := seedAPIKey(t, ts.db, ts.ownerID, "phone")

	// Before the reset both keys pass REST.
	for _, k := range []*store.CreatedAPIKey{k1, k2} {
		if code := apiGetDecks(t, rest, k.Plaintext); code != http.StatusOK {
			t.Fatalf("pre-reset GET /api/v1/decks with key %q = %d, want 200", k.Key.Name, code)
		}
	}

	// Full forgot-password flow: request the mail → set a new password with the real token from the mail.
	plain, _ := issueResetToken(t, ts)
	reset := resetPasswordJSON(t, ts, plain, "N3wSup3rSecret!")
	if reset.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/reset-password status = %d, want 200 (body %s)", reset.Code, snippet(reset.Body.String()))
	}

	// Both old keys: invalid in the database, and 401 against the real REST chain (consistent with the existing key revocation semantics).
	assertAllKeysRevoked(t, ts.db, ts.ownerID)
	for _, k := range []*store.CreatedAPIKey{k1, k2} {
		if code := apiGetDecks(t, rest, k.Plaintext); code != http.StatusUnauthorized {
			t.Errorf("post-reset GET /api/v1/decks with key %q = %d, want 401", k.Key.Name, code)
		}
	}

	// Session invalidation is not broken: the pre-reset session must be invalidated, with protected pages redirecting to login.
	if rec := getWithCookies(t, ts.srv, "/admin/users", ts.cookies); rec.Code != http.StatusSeeOther {
		t.Errorf("old session after reset = %d, want 303 to login", rec.Code)
	}
}

// TestAdminPasswordResetRevokesAPIKeys covers an admin resetting someone else's password: all of the target user's keys are revoked too.
func TestAdminPasswordResetRevokesAPIKeys(t *testing.T) {
	ts := newSecurityServer(t, true)
	targetID := createTargetUser(t, ts)
	rest := newKeysAPI(t, ts.db)
	k1 := seedAPIKey(t, ts.db, targetID, "target-a")
	k2 := seedAPIKey(t, ts.db, targetID, "target-b")

	if code := apiGetDecks(t, rest, k1.Plaintext); code != http.StatusOK {
		t.Fatalf("pre-reset GET /api/v1/decks = %d, want 200", code)
	}

	rec := jsonRequest(t, ts.srv, http.MethodPost, "/api/v1/admin/users/"+userPublicID(t, ts.db, targetID)+"/password",
		`{"confirm":true}`, ts.cookies, ts.csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/admin/users/:id/password status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	assertAllKeysRevoked(t, ts.db, targetID)
	for _, k := range []*store.CreatedAPIKey{k1, k2} {
		if code := apiGetDecks(t, rest, k.Plaintext); code != http.StatusUnauthorized {
			t.Errorf("post-admin-reset GET /api/v1/decks with key %q = %d, want 401", k.Key.Name, code)
		}
	}
}

// TestSelfPasswordChangeKeepsAPIKeys is the negative control: a user changing their own password (/settings/password)
// never revokes their own keys. An incidental change that narrows this will turn this test red.
func TestSelfPasswordChangeKeepsAPIKeys(t *testing.T) {
	ts := newSecurityServer(t, true)
	member, err := ts.srv.accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: "member", Email: "member@example.com", Password: "Sup3rSecret!", Role: store.RoleUser, Locale: "en",
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	memberID := member.ID
	cookies, csrf := loginJSON(t, ts.srv, ts.db, "member", "Sup3rSecret!")
	rest := newKeysAPI(t, ts.db)
	k1 := seedAPIKey(t, ts.db, memberID, "cli")

	if code := apiGetDecks(t, rest, k1.Plaintext); code != http.StatusOK {
		t.Fatalf("pre-change GET /api/v1/decks = %d, want 200", code)
	}

	rec := jsonRequest(t, ts.srv, http.MethodPatch, "/api/v1/settings/password",
		`{"old_password":"Sup3rSecret!","new_password":"N3wSup3rSecret!"}`, cookies, csrf)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PATCH /api/v1/settings/password status = %d, want 204 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	keys, err := store.NewAPIKeyStore(ts.db).ListByUser(context.Background(), memberID)
	if err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}
	if len(keys) != 1 || keys[0].RevokedAt != nil {
		t.Errorf("self password change revoked the user's own api key; want it kept")
	}
	if code := apiGetDecks(t, rest, k1.Plaintext); code != http.StatusOK {
		t.Errorf("old api key after self change = %d, want 200", code)
	}
}
