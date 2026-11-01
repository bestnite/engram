package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestDeleteKeyIsScopedToCaller pins the scoping of DELETE /api/v1/keys/{id} at the handler that
// enforces it: the caller revokes their own key, while another account's key id is answered as
// not_found and left active (the existence of that key does not leak).
func TestDeleteKeyIsScopedToCaller(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	caller := seedUser(t, env.db, "key_revoke_caller", store.RoleUser)
	other := seedUser(t, env.db, "key_revoke_other", store.RoleUser)
	authKey := seedKey(t, env.keys, caller.ID, []string{store.ScopeKeys}, nil)
	ownKey := seedKey(t, env.keys, caller.ID, []string{store.ScopeRead}, nil)
	otherKey := seedKey(t, env.keys, other.ID, []string{store.ScopeRead}, nil)

	t.Run("own key is revoked", func(t *testing.T) {
		code, raw := doJSON(t, router, http.MethodDelete, "/api/v1/keys/"+ownKey.Key.PublicID, authKey.Plaintext, "")
		if code != http.StatusOK {
			t.Fatalf("DELETE own key = %d, want 200 (body %s)", code, raw)
		}
		var body struct {
			Revoked bool   `json:"revoked"`
			ID      string `json:"id"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode response: %v (%s)", err, raw)
		}
		if !body.Revoked || body.ID != ownKey.Key.PublicID {
			t.Errorf("response = %+v, want revoked=true and id=%s", body, ownKey.Key.PublicID)
		}
		var stored store.APIKey
		if err := env.db.First(&stored, ownKey.Key.ID).Error; err != nil {
			t.Fatalf("load own key: %v", err)
		}
		if stored.RevokedAt == nil {
			t.Errorf("own key revoked_at = nil, want the revocation written")
		}
	})

	t.Run("another account's key is not revoked", func(t *testing.T) {
		code, raw := doJSON(t, router, http.MethodDelete, "/api/v1/keys/"+otherKey.Key.PublicID, authKey.Plaintext, "")
		if code != http.StatusNotFound {
			t.Fatalf("DELETE another account's key = %d, want 404 (body %s)", code, raw)
		}
		if got := decodeErrorCode(t, raw); got != CodeNotFound {
			t.Errorf("error code = %q, want %q", got, CodeNotFound)
		}
		var stored store.APIKey
		if err := env.db.First(&stored, otherKey.Key.ID).Error; err != nil {
			t.Fatalf("load other key: %v", err)
		}
		if stored.RevokedAt != nil {
			t.Errorf("other account's key revoked_at = %v, want it left active", stored.RevokedAt)
		}
		// The key still authenticates, so the refusal did not revoke it out of band either.
		if code, raw := doJSON(t, router, http.MethodGet, "/api/v1/decks", otherKey.Plaintext, ""); code != http.StatusOK {
			t.Errorf("other account's key after the refused revoke = %d, want 200 (body %s)", code, raw)
		}
	})
}
